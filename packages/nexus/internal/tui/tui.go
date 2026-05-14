package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

const headerLines = 2
const footerLines = 4

type panelKind int

const (
	panelNone panelKind = iota
	panelConnect
	panelSpotlight
	panelSync
)

const (
	promptNone      = ""
	promptSpotPort  = "spot_port"
	promptSyncLocal = "sync_local"
	promptSyncDir   = "sync_dir"
	promptForkChild = "fork_child"
)

type syncRow struct {
	SessionID   string
	WorkspaceID string
	LocalPath   string
	Status      string
	Direction   string
}

type connReadyMsg struct {
	conn *websocket.Conn
}

type connErrMsg struct {
	err error
}

type workspacesMsg struct {
	workspaces []workspace.Workspace
}

type workspacesErrMsg struct {
	err error
}

type refreshTickMsg struct{}

type detailLoadedMsg struct {
	ws workspace.Workspace
}

type detailErrMsg struct {
	err error
}

type mutationDoneMsg struct {
	err error
}

type spotlightsDataMsg struct {
	forwards []*spotlight.Forward
	err      error
}

type syncListDataMsg struct {
	rows []syncRow
	err  error
}

type createDoneMsg struct {
	err error
}

type forkDoneMsg struct {
	err error
}

type shellReturnedMsg struct {
	err  error
	wsID string // ID of the workspace whose shell just exited
}

// Model is the root Bubble Tea model for the Nexus TUI.
type Model struct {
	list            list.Model
	conn            *websocket.Conn
	detail          *workspace.Workspace
	daemonOK        bool
	quitting        bool
	statusLine      string
	confirmDelete   bool
	pendingDeleteID string
	width           int
	height          int
	listWidth       int
	listHeight      int

	panel      panelKind
	showHelp   bool
	spotForwards []*spotlight.Forward
	spotSel    int
	syncRows   []syncRow
	syncSel    int

	prompt      string
	promptInput textinput.Model

	createMode bool
	createStep int
	nameTI     textinput.Model
	repoTI     textinput.Model
	refTI      textinput.Model

	pendingSyncPath string

	// session tabs — workspaces attached at least once this TUI session
	tabs        []string // workspace IDs in order
	activeTabWS string   // last attached workspace ID
	autoAttach  bool     // --auto-attach flag
	autoAttachDone bool  // prevent multiple auto-attaches
}

// tabBarOffset returns the number of extra lines consumed by the tab bar
// (1 when tabs are present, 0 otherwise).
func (m Model) tabBarOffset() int {
	if len(m.tabs) == 0 {
		return 0
	}
	return 1
}

// updateListSize recalculates listHeight from terminal dimensions and calls
// SetSize on the bubbles list. Must be called whenever m.height or m.tabs
// changes.
func (m *Model) updateListSize() {
	if m.height == 0 {
		return
	}
	h := max(m.height-headerLines-footerLines-4-m.tabBarOffset(), 6)
	m.listHeight = h
	m.list.SetSize(m.listWidth, h)
}

func (m Model) blocksOverlayInput() bool {
	if m.showHelp {
		return true
	}
	if m.panel != panelNone {
		return true
	}
	if m.prompt != promptNone {
		return true
	}
	if m.createMode {
		return true
	}
	if m.confirmDelete {
		return true
	}
	return false
}

// Options configures a TUI run.
type Options struct {
	// AutoAttach re-attaches to the last active workspace on startup
	// (if it is in running state). Requires --auto-attach flag; NOT default.
	AutoAttach bool
}

// Run starts the interactive TUI until the user quits.
func Run(opts ...Options) error {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	p := tea.NewProgram(NewModel(o), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// NewModel constructs an initial model (connection opens during Init).
func NewModel(opts ...Options) Model {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	delegate := styledDelegate()
	l := newWorkspaceList(delegate, 80, 20)
	l.SetFilteringEnabled(true)

	pi := textinput.New()
	pi.Prompt = "> "
	pi.CharLimit = 512

	nt, rt, rf := newCreateInputs()

	// Restore session state from disk.
	ss := loadSessionState()

	return Model{
		list:        l,
		statusLine:  "connecting…",
		promptInput: pi,
		nameTI:      nt,
		repoTI:      rt,
		refTI:       rf,
		tabs:        ss.Tabs,
		activeTabWS: ss.Active,
		autoAttach:  o.AutoAttach,
	}
}

func newCreateInputs() (name, repo, ref textinput.Model) {
	name = textinput.New()
	name.Placeholder = "name (required)"
	name.CharLimit = 120
	name.Width = 56

	repo = textinput.New()
	repo.Placeholder = "repo path on engine (required)"
	repo.CharLimit = 512
	repo.Width = 56

	ref = textinput.New()
	ref.Placeholder = "ref / branch (optional)"
	ref.CharLimit = 200
	ref.Width = 56
	return name, repo, ref
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return openDaemonConn()
}

func openDaemonConn() tea.Cmd {
	return func() tea.Msg {
		c, err := rpc.EnsureDaemon()
		if err != nil {
			return connErrMsg{err: err}
		}
		return connReadyMsg{conn: c}
	}
}

func (m Model) refreshCmd() tea.Cmd {
	c := m.conn
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		ws, err := fetchWorkspaces(c)
		if err != nil {
			return workspacesErrMsg{err: err}
		}
		return workspacesMsg{workspaces: ws}
	}
}

func fetchWorkspaces(c *websocket.Conn) ([]workspace.Workspace, error) {
	var result struct {
		Workspaces []*workspace.Workspace `json:"workspaces"`
	}
	if err := rpc.Do(c, "workspace.list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	out := make([]workspace.Workspace, 0, len(result.Workspaces))
	for _, p := range result.Workspaces {
		if p == nil {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

func (m Model) loadDetailCmd(id string) tea.Cmd {
	c := m.conn
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		var result struct {
			Workspace *workspace.Workspace `json:"workspace"`
		}
		if err := rpc.Do(c, "workspace.info", map[string]any{"id": id}, &result); err != nil {
			return detailErrMsg{err: err}
		}
		if result.Workspace == nil {
			return detailErrMsg{err: fmt.Errorf("workspace.info: empty workspace")}
		}
		return detailLoadedMsg{ws: *result.Workspace}
	}
}

func (m Model) rpcVoidCmd(method string, id string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := rpc.Do(c, method, map[string]any{"id": id}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) loadSpotlightsCmd(wsID string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return spotlightsDataMsg{err: fmt.Errorf("not connected")}
		}
		var result struct {
			Forwards []*spotlight.Forward `json:"forwards"`
		}
		if err := rpc.Do(c, "spotlight.list", map[string]any{"workspaceId": wsID}, &result); err != nil {
			return spotlightsDataMsg{err: err}
		}
		return spotlightsDataMsg{forwards: result.Forwards}
	}
}

func (m Model) loadSyncListCmd(wsID string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return syncListDataMsg{err: fmt.Errorf("not connected")}
		}
		var result struct {
			Sessions []struct {
				SessionID   string `json:"sessionId"`
				WorkspaceID string `json:"workspaceId"`
				LocalPath   string `json:"localPath"`
				Status      string `json:"status"`
				Direction   string `json:"direction"`
			} `json:"sessions"`
		}
		if err := rpc.Do(c, "workspace.sync-list", map[string]any{"workspaceId": wsID}, &result); err != nil {
			return syncListDataMsg{err: err}
		}
		rows := make([]syncRow, 0, len(result.Sessions))
		for _, s := range result.Sessions {
			rows = append(rows, syncRow{
				SessionID:   s.SessionID,
				WorkspaceID: s.WorkspaceID,
				LocalPath:   s.LocalPath,
				Status:      s.Status,
				Direction:   s.Direction,
			})
		}
		return syncListDataMsg{rows: rows}
	}
}

func (m Model) startSpotlightCmd(wsID string, remotePort int) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		spec := spotlight.ExposeSpec{
			WorkspaceID: wsID,
			LocalPort:   remotePort,
			RemotePort:  remotePort,
			Source:      spotlight.ForwardSourceUser,
		}
		var result struct {
			Forward *spotlight.Forward `json:"forward"`
		}
		if err := rpc.Do(c, "spotlight.start", map[string]any{"workspaceId": wsID, "spec": spec}, &result); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) removeForwardCmd(wsID, forwardID string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := rpc.Do(c, "workspace.ports.remove", map[string]any{"workspaceId": wsID, "forwardId": forwardID}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) syncStartCmd(wsID, localPath, direction string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := rpc.Do(c, "workspace.sync-start", map[string]any{
			"workspaceId": wsID,
			"localPath":   localPath,
			"direction":   direction,
		}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) syncStopSessionCmd(sessionID string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := rpc.Do(c, "workspace.sync-stop", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) syncPauseCmd(sessionID string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := rpc.Do(c, "workspace.sync-pause", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) syncResumeCmd(sessionID string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := rpc.Do(c, "workspace.sync-resume", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) forkCmd(parentID, childName string) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return forkDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := rpc.Do(c, "workspace.fork", map[string]any{
			"id":                 parentID,
			"childWorkspaceName": childName,
		}, nil); err != nil {
			return forkDoneMsg{err: err}
		}
		return forkDoneMsg{err: nil}
	}
}

func (m Model) createWorkspaceCmd(spec workspace.CreateSpec) tea.Cmd {
	c := m.conn
	return func() tea.Msg {
		if c == nil {
			return createDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := rpc.Do(c, "workspace.create", map[string]any{"spec": spec}, nil); err != nil {
			return createDoneMsg{err: err}
		}
		return createDoneMsg{err: nil}
	}
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.listWidth = max(msg.Width-4, 24)
		m.updateListSize()
		return m, nil

	case connReadyMsg:
		m.conn = msg.conn
		m.daemonOK = true
		m.statusLine = ""
		return m, tea.Batch(
			m.refreshCmd(),
			tickRefresh(),
		)

	case connErrMsg:
		m.daemonOK = false
		m.statusLine = msg.err.Error()
		return m, nil

	case workspacesMsg:
		m.daemonOK = true
		sel := selectedWorkspaceID(m.list.SelectedItem())
		m.list.SetItems(workspacesToItems(msg.workspaces))
		if sel != "" {
			reselectByID(&m.list, sel)
		}
		if m.detail != nil {
			id := m.detail.ID
			for _, w := range msg.workspaces {
				if w.ID == id {
					wcopy := w
					m.detail = &wcopy
					break
				}
			}
		}
		// Auto-attach to last active workspace on first load (--auto-attach only).
		if m.autoAttach && !m.autoAttachDone && m.activeTabWS != "" {
			m.autoAttachDone = true
			for _, w := range msg.workspaces {
				if w.ID == m.activeTabWS && w.State == workspace.StateRunning {
					return m, m.shellExecCmd(m.activeTabWS)
				}
			}
		}
		return m, nil

	case workspacesErrMsg:
		m.daemonOK = false
		m.statusLine = msg.err.Error()
		return m, nil

	case refreshTickMsg:
		if m.quitting {
			return m, nil
		}
		if m.conn != nil && !m.blocksOverlayInput() {
			return m, m.refreshCmd()
		}
		return m, nil

	case detailLoadedMsg:
		w := msg.ws
		m.detail = &w
		m.statusLine = ""
		return m, nil

	case detailErrMsg:
		m.statusLine = msg.err.Error()
		return m, nil

	case mutationDoneMsg:
		if msg.err != nil {
			m.statusLine = msg.err.Error()
			return m, nil
		}
		m.statusLine = ""
		if m.panel == panelSpotlight && m.detail != nil {
			return m, m.loadSpotlightsCmd(m.detail.ID)
		}
		if m.panel == panelSync && m.detail != nil {
			return m, m.loadSyncListCmd(m.detail.ID)
		}
		return m, m.refreshCmd()

	case spotlightsDataMsg:
		if msg.err != nil {
			m.statusLine = msg.err.Error()
			return m, nil
		}
		m.spotForwards = msg.forwards
		if m.spotSel >= len(m.spotForwards) {
			m.spotSel = max(0, len(m.spotForwards)-1)
		}
		return m, nil

	case syncListDataMsg:
		if msg.err != nil {
			m.statusLine = msg.err.Error()
			return m, nil
		}
		m.syncRows = msg.rows
		if m.syncSel >= len(m.syncRows) {
			m.syncSel = max(0, len(m.syncRows)-1)
		}
		return m, nil

	case createDoneMsg:
		m.createMode = false
		m.createStep = 0
		if msg.err != nil {
			m.statusLine = msg.err.Error()
			return m, nil
		}
		m.statusLine = "workspace created"
		return m, m.refreshCmd()

	case forkDoneMsg:
		m.prompt = promptNone
		m.promptInput.Blur()
		if msg.err != nil {
			m.statusLine = msg.err.Error()
			return m, nil
		}
		m.statusLine = "fork created"
		pid := ""
		if m.detail != nil {
			pid = m.detail.ID
		}
		if pid != "" {
			return m, tea.Batch(m.refreshCmd(), m.loadDetailCmd(pid))
		}
		return m, m.refreshCmd()

	case shellReturnedMsg:
		if msg.err != nil && msg.err.Error() != "" && msg.err.Error() != "exit status 0" {
			// ExecProcess may pass non-nil for exit code !=0; still refresh.
			m.statusLine = msg.err.Error()
		}
		// Record in session tabs (addTab is idempotent).
		if msg.wsID != "" {
			m.tabs = addTab(m.tabs, msg.wsID)
			m.activeTabWS = msg.wsID
			m.updateListSize()
			saveSessionState(sessionState{Tabs: m.tabs, Active: msg.wsID})
		}
		return m, m.refreshCmd()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.createMode {
		var cmd tea.Cmd
		switch m.createStep {
		case 0:
			m.nameTI, cmd = m.nameTI.Update(msg)
		case 1:
			m.repoTI, cmd = m.repoTI.Update(msg)
		case 2:
			m.refTI, cmd = m.refTI.Update(msg)
		}
		return m, cmd
	}
	if m.prompt != promptNone {
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		if m.conn != nil {
			_ = m.conn.Close()
			m.conn = nil
		}
		return m, tea.Quit
	}

	if m.showHelp {
		if msg.String() == "esc" || msg.String() == "?" {
			m.showHelp = false
		}
		return m, nil
	}

	if msg.String() == "?" {
		m.showHelp = true
		return m, nil
	}

	// Tab jump: 1–9 selects the Nth session tab in the workspace list.
	if !m.blocksOverlayInput() {
		key := msg.String()
		if len(key) == 1 && key >= "1" && key <= "9" {
			idx := int(key[0]-'0') - 1
			if idx < len(m.tabs) {
				wsID := m.tabs[idx]
				m.activeTabWS = wsID
				m.detail = nil
				m.panel = panelNone
				reselectByID(&m.list, wsID)
			}
			return m, nil
		}
	}

	if m.createMode {
		return m.handleCreateKeys(msg)
	}

	if m.prompt != promptNone {
		return m.handlePromptKeys(msg)
	}

	if m.panel == panelConnect {
		if msg.String() == "esc" || msg.String() == "c" {
			m.panel = panelNone
		}
		return m, nil
	}
	if m.panel == panelSpotlight {
		return m.handleSpotlightKeys(msg)
	}
	if m.panel == panelSync {
		return m.handleSyncKeys(msg)
	}

	if msg.String() == "esc" {
		if m.confirmDelete {
			m.confirmDelete = false
			m.pendingDeleteID = ""
			return m, nil
		}
		if m.detail != nil {
			m.detail = nil
			return m, nil
		}
	}

	if m.confirmDelete {
		switch strings.ToLower(msg.String()) {
		case "y":
			id := m.pendingDeleteID
			m.confirmDelete = false
			m.pendingDeleteID = ""
			if m.detail != nil && m.detail.ID == id {
				m.detail = nil
			}
			return m, m.rpcVoidCmd("workspace.remove", id)
		case "n":
			m.confirmDelete = false
			m.pendingDeleteID = ""
			return m, nil
		default:
			m.confirmDelete = false
			m.pendingDeleteID = ""
			return m, nil
		}
	}

	switch msg.String() {
	case "r":
		return m, m.refreshCmd()
	}

	if m.detail != nil {
		return m.handleDetailKeys(msg)
	}

	return m.handleListKeys(msg)
}

func (m Model) handleCreateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.createMode = false
		m.createStep = 0
		m.nameTI.Blur()
		m.repoTI.Blur()
		m.refTI.Blur()
		return m, nil
	case "tab":
		m.createStep = (m.createStep + 1) % 3
		m, cmd := m.refocusCreateStep()
		return m, cmd
	case "shift+tab":
		m.createStep = (m.createStep + 2) % 3
		m, cmd := m.refocusCreateStep()
		return m, cmd
	case "enter":
		switch m.createStep {
		case 0:
			if strings.TrimSpace(m.nameTI.Value()) == "" {
				m.statusLine = "name is required"
				return m, nil
			}
			m.createStep = 1
			m.nameTI.Blur()
			m.repoTI.Blur()
			return m, m.repoTI.Focus()
		case 1:
			if strings.TrimSpace(m.repoTI.Value()) == "" {
				m.statusLine = "repo is required"
				return m, nil
			}
			m.createStep = 2
			m.repoTI.Blur()
			m.refTI.Blur()
			return m, m.refTI.Focus()
		default:
			spec := workspace.CreateSpec{
				Repo:          strings.TrimSpace(m.repoTI.Value()),
				Ref:           strings.TrimSpace(m.refTI.Value()),
				WorkspaceName: strings.TrimSpace(m.nameTI.Value()),
				AgentProfile:  "default",
				Policy:        workspace.Policy{},
			}
			m.createMode = false
			m.createStep = 0
			m.nameTI.Blur()
			m.repoTI.Blur()
			m.refTI.Blur()
			nt, rt, rf := newCreateInputs()
			m.nameTI, m.repoTI, m.refTI = nt, rt, rf
			return m, m.createWorkspaceCmd(spec)
		}
	default:
		var cmd tea.Cmd
		switch m.createStep {
		case 0:
			m.nameTI, cmd = m.nameTI.Update(msg)
		case 1:
			m.repoTI, cmd = m.repoTI.Update(msg)
		case 2:
			m.refTI, cmd = m.refTI.Update(msg)
		}
		return m, cmd
	}
}

func (m Model) refocusCreateStep() (Model, tea.Cmd) {
	m.nameTI.Blur()
	m.repoTI.Blur()
	m.refTI.Blur()
	switch m.createStep {
	case 0:
		return m, m.nameTI.Focus()
	case 1:
		return m, m.repoTI.Focus()
	default:
		return m, m.refTI.Focus()
	}
}

func (m Model) handlePromptKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.prompt = promptNone
		m.promptInput.SetValue("")
		m.promptInput.Blur()
		m.pendingSyncPath = ""
		return m, nil
	case "enter":
		line := strings.TrimSpace(m.promptInput.Value())
		m.promptInput.SetValue("")
		m.promptInput.Blur()
		switch m.prompt {
		case promptForkChild:
			if line == "" || m.detail == nil {
				m.prompt = promptNone
				return m, nil
			}
			m.prompt = promptNone
			return m, m.forkCmd(m.detail.ID, line)
		case promptSpotPort:
			m.prompt = promptNone
			if m.detail == nil {
				return m, nil
			}
			p, err := strconv.Atoi(line)
			if err != nil || p <= 0 || p > 65535 {
				m.statusLine = "invalid port"
				return m, m.loadSpotlightsCmd(m.detail.ID)
			}
			return m, m.startSpotlightCmd(m.detail.ID, p)
		case promptSyncLocal:
			if line == "" {
				m.prompt = promptNone
				return m, nil
			}
			m.pendingSyncPath = line
			m.prompt = promptSyncDir
			m.promptInput.Placeholder = "direction: bidirectional | up | down"
			return m, m.promptInput.Focus()
		case promptSyncDir:
			m.prompt = promptNone
			path := m.pendingSyncPath
			m.pendingSyncPath = ""
			dir := line
			if dir == "" {
				dir = "bidirectional"
			}
			if m.detail == nil {
				return m, nil
			}
			return m, m.syncStartCmd(m.detail.ID, path, dir)
		default:
			m.prompt = promptNone
			return m, nil
		}
	default:
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}
}

func (m Model) handleSpotlightKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "l":
		m.panel = panelNone
		return m, nil
	case "n":
		m.prompt = promptSpotPort
		m.promptInput.Placeholder = "remote port"
		return m, m.promptInput.Focus()
	case "x":
		if m.detail == nil || len(m.spotForwards) == 0 {
			return m, nil
		}
		if m.spotSel < 0 || m.spotSel >= len(m.spotForwards) {
			return m, nil
		}
		f := m.spotForwards[m.spotSel]
		if f == nil {
			return m, nil
		}
		return m, m.removeForwardCmd(m.detail.ID, f.ID)
	case "up", "k":
		if m.spotSel > 0 {
			m.spotSel--
		}
		return m, nil
	case "down", "j":
		if m.spotSel < len(m.spotForwards)-1 {
			m.spotSel++
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleSyncKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "y":
		m.panel = panelNone
		return m, nil
	case "n":
		m.prompt = promptSyncLocal
		m.promptInput.Placeholder = "local path to sync"
		return m, m.promptInput.Focus()
	case "x":
		if m.detail == nil || len(m.syncRows) == 0 {
			return m, nil
		}
		if m.syncSel < 0 || m.syncSel >= len(m.syncRows) {
			return m, nil
		}
		sid := m.syncRows[m.syncSel].SessionID
		return m, m.syncStopSessionCmd(sid)
	case "p":
		if len(m.syncRows) == 0 || m.detail == nil {
			return m, nil
		}
		sid := m.syncRows[m.syncSel].SessionID
		return m, m.syncPauseCmd(sid)
	case "r":
		if len(m.syncRows) == 0 || m.detail == nil {
			return m, nil
		}
		sid := m.syncRows[m.syncSel].SessionID
		return m, m.syncResumeCmd(sid)
	case "up", "k":
		if m.syncSel > 0 {
			m.syncSel--
		}
		return m, nil
	case "down", "j":
		if m.syncSel < len(m.syncRows)-1 {
			m.syncSel++
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleDetailKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		m.detail = nil
		return m, nil
	}
	id := m.detail.ID
	switch msg.String() {
	case "r":
		return m, m.refreshCmd()
	case "s":
		return m, m.rpcVoidCmd("workspace.start", id)
	case "x":
		return m, m.rpcVoidCmd("workspace.stop", id)
	case "d":
		m.confirmDelete = true
		m.pendingDeleteID = id
		return m, nil
	case "c":
		m.panel = panelConnect
		return m, nil
	case "l":
		m.panel = panelSpotlight
		m.spotSel = 0
		return m, m.loadSpotlightsCmd(id)
	case "y":
		m.panel = panelSync
		m.syncSel = 0
		return m, m.loadSyncListCmd(id)
	case "t":
		return m.runShellExec(id)
	case "f":
		m.prompt = promptForkChild
		m.promptInput.Placeholder = "child workspace name"
		return m, m.promptInput.Focus()
	}
	return m, nil
}

// shellExecCmd returns a tea.Cmd that runs nexus workspace shell <wsID> via
// ExecProcess. The TUI resumes when the shell exits, delivering shellReturnedMsg.
func (m Model) shellExecCmd(wsID string) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = "nexus"
	}
	c := exec.Command(exe, "workspace", "shell", wsID)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = os.Environ()
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return shellReturnedMsg{err: err, wsID: wsID}
	})
}

func (m Model) runShellExec(wsID string) (tea.Model, tea.Cmd) {
	m.activeTabWS = wsID
	// Pre-register tab so it appears immediately after ExecProcess returns.
	before := len(m.tabs)
	m.tabs = addTab(m.tabs, wsID)
	if len(m.tabs) != before {
		m.updateListSize()
	}
	saveSessionState(sessionState{Tabs: m.tabs, Active: wsID})
	return m, m.shellExecCmd(wsID)
}

func (m Model) handleListKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "n" || msg.String() == "+" {
		m.createMode = true
		m.createStep = 0
		nt, rt, rf := newCreateInputs()
		m.nameTI, m.repoTI, m.refTI = nt, rt, rf
		return m, m.nameTI.Focus()
	}

	if msg.String() == "t" {
		it := m.list.SelectedItem()
		if it == nil {
			return m, nil
		}
		wi, ok := it.(workspaceItem)
		if !ok {
			return m, nil
		}
		return m.runShellExec(wi.w.ID)
	}

	switch msg.String() {
	case "enter":
		it := m.list.SelectedItem()
		if it == nil {
			return m, nil
		}
		wi, ok := it.(workspaceItem)
		if !ok {
			return m, nil
		}
		return m, m.loadDetailCmd(wi.w.ID)

	case "s", "x", "d":
		it := m.list.SelectedItem()
		if it == nil {
			return m, nil
		}
		wi, ok := it.(workspaceItem)
		if !ok {
			return m, nil
		}
		wid := wi.w.ID
		switch msg.String() {
		case "s":
			return m, m.rpcVoidCmd("workspace.start", wid)
		case "x":
			return m, m.rpcVoidCmd("workspace.stop", wid)
		case "d":
			m.confirmDelete = true
			m.pendingDeleteID = wid
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func tickRefresh() tea.Cmd {
	return tea.Every(5*time.Second, func(time.Time) tea.Msg {
		return refreshTickMsg{}
	})
}

func selectedWorkspaceID(it list.Item) string {
	if it == nil {
		return ""
	}
	wi, ok := it.(workspaceItem)
	if !ok {
		return ""
	}
	return wi.w.ID
}

func reselectByID(l *list.Model, id string) {
	items := l.Items()
	for i, it := range items {
		wi, ok := it.(workspaceItem)
		if ok && wi.w.ID == id {
			l.Select(i)
			return
		}
	}
}

// View implements tea.Model.
func (m Model) View() string {
	statusDaemon := statusErrStyle.Render("disconnected")
	if m.quitting {
		statusDaemon = mutedStyle.Render("exiting")
	} else if m.conn != nil && m.daemonOK {
		statusDaemon = statusOkStyle.Render("connected")
	} else if m.conn != nil && !m.daemonOK {
		statusDaemon = warningStyle.Render("degraded")
	}

	headerTop := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("Nexus"),
		mutedStyle.Render(" · daemon "),
		statusDaemon,
	)
	headerBottom := ""
	if strings.TrimSpace(m.statusLine) != "" {
		headerBottom = mutedStyle.Render(truncate(m.statusLine, max(m.width-4, 40)))
	}

	body := ""
	switch {
	case m.createMode:
		body = renderCreateWizard(&m, m.listWidth)
	case m.showHelp:
		body = renderHelp(m.listWidth)
	case m.panel == panelConnect && m.detail != nil:
		body = renderConnectPanel(m.detail, m.listWidth)
	case m.panel == panelSpotlight && m.detail != nil:
		body = renderSpotlightPanel(&m, m.listWidth)
	case m.panel == panelSync && m.detail != nil:
		body = renderSyncPanel(&m, m.listWidth)
	case m.detail != nil:
		body = renderWorkspaceDetail(m.detail, m.listWidth)
	default:
		body = m.list.View()
	}

	if m.prompt != promptNone {
		body = lipgloss.JoinVertical(lipgloss.Left,
			body,
			"",
			titleStyle.Render("Input"),
			m.promptInput.View(),
		)
	}

	help := mutedStyle.Render(
		"q quit · ? help · enter detail · t terminal · esc back · r refresh · n new · / filter · s start · x stop · d delete · 1-9 tab jump",
	)
	dhelp := mutedStyle.Render(
		"q quit · ? · esc · r · c connect · l spotlight · y sync · t terminal · f fork · s/x/d",
	)
	footerHelp := help
	if m.detail != nil && m.panel == panelNone && !m.createMode && !m.showHelp {
		footerHelp = dhelp
	}
	if m.panel == panelSpotlight {
		footerHelp = mutedStyle.Render("esc/l close · n new port · x remove selected · j/k move")
	}
	if m.panel == panelSync {
		footerHelp = mutedStyle.Render("esc/y close · n new · x stop · p pause · r resume · j/k move")
	}
	if m.panel == panelConnect {
		footerHelp = mutedStyle.Render("esc/c close connect panel")
	}
	if m.createMode {
		footerHelp = mutedStyle.Render("tab switch field · enter next/submit · esc cancel")
	}

	confirm := ""
	if m.confirmDelete {
		confirm = warningStyle.Render(fmt.Sprintf("Delete workspace %s? [y/N]", truncate(m.pendingDeleteID, 36)))
	}

	footer := lipgloss.JoinVertical(lipgloss.Left, confirm, footerHelp)
	// Build the vertical stack. Insert the tab bar when present.
	rows := []string{headerTop, headerBottom}
	if tabBar := m.renderTabBar(); tabBar != "" {
		rows = append(rows, tabSepStyle.Render(strings.Repeat("─", max(m.width-4, 20))))
		rows = append(rows, tabBar)
	}
	rows = append(rows, "", body, "", footer)

	box := lipgloss.NewStyle().
		MaxWidth(m.width).
		Padding(0, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, rows...))

	return box
}

// renderTabBar renders the session tab bar line.
// Returns an empty string when there are no tabs.
func (m Model) renderTabBar() string {
	if len(m.tabs) == 0 {
		return ""
	}

	var parts []string
	for i, wsID := range m.tabs {
		if i >= 9 {
			break
		}
		num := fmt.Sprintf("[%d]", i+1)

		// Look up workspace name and state from the current list.
		name := truncate(wsID, 14)
		badge := mutedStyle.Render("○")
		for _, it := range m.list.Items() {
			wi, ok := it.(workspaceItem)
			if !ok || wi.w.ID != wsID {
				continue
			}
			if wi.w.WorkspaceName != "" {
				name = truncate(wi.w.WorkspaceName, 14)
			}
			badge = tabStateBadge(wi.w.State)
			break
		}

		label := num + " " + name + " " + badge
		if wsID == m.activeTabWS {
			parts = append(parts, activeTabStyle.Render(label))
		} else {
			parts = append(parts, inactiveTabStyle.Render(label))
		}
	}

	// [+] hint — press n or + to create a new workspace.
	parts = append(parts, mutedStyle.Render("[+]"))

	return " " + strings.Join(parts, "  ")
}

// tabStateBadge returns a compact coloured state symbol for use in the tab bar.
func tabStateBadge(state workspace.State) string {
	switch state {
	case workspace.StateRunning:
		return statusOkStyle.Render("●")
	case workspace.StateStarting, workspace.StateSnapshotting, workspace.StateRestored:
		return warningStyle.Render("⟳")
	case workspace.StateStopped, workspace.StatePaused, workspace.StateCreated:
		return mutedStyle.Render("○")
	default:
		return mutedStyle.Render("?")
	}
}

func renderCreateWizard(m *Model, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("New workspace"))
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Name"))
	fmt.Fprintf(&b, "%s\n\n", m.nameTI.View())
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Repo path"))
	fmt.Fprintf(&b, "%s\n\n", m.repoTI.View())
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Ref"))
	fmt.Fprintf(&b, "%s\n", m.refTI.View())
	return lipgloss.NewStyle().MaxWidth(width).Render(b.String())
}

func renderHelp(width int) string {
	s := `Key reference

Global
  q           Quit
  ?           Toggle this help
  r           Refresh workspace list
  /           Filter workspaces (when list is focused)
  1-9         Jump-select session tab N in the workspace list

Main list
  n / +       New workspace (wizard)
  enter       Open workspace detail
  t           Attach terminal (nexus workspace shell)
  s x d       Start / stop / delete selected workspace

Detail view
  esc enter   Back to list
  c           Connect (SSH / editor hints)
  l           Spotlight forwards
  y           Volume sync sessions
  t           Terminal (runs: nexus workspace shell)
  f           Fork workspace
  s x d       Start / stop / delete

Tab bar (appears after first terminal attach)
  Persistent across sessions via ~/.local/state/nexus/tui-sessions.json
  Use --auto-attach flag to re-enter last shell on startup
`
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func renderConnectPanel(ws *workspace.Workspace, width int) string {
	info := buildConnectInstructions(ws)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Connect (SSH / editors)"))
	if info.ProxyJump != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("ProxyJump"), info.ProxyJump)
	}
	if info.GuestTarget != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("SSH target"), info.GuestTarget)
	}
	if info.HostAlias != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("SSH config Host"), info.HostAlias)
	}
	fmt.Fprintf(&b, "\n%s\n", detailKeyStyle.Render("SSH command (copy)"))
	fmt.Fprintf(&b, "%s\n\n", detailValStyle.Render(info.SSHCommand))
	if info.ProcessShellHint != "" {
		fmt.Fprintf(&b, "%s\n%s\n\n", mutedStyle.Render(info.ProcessShellHint), detailValStyle.Render(strings.TrimSpace(info.SSHCommand)))
	}
	fmt.Fprintf(&b, "%s\n%s\n\n", detailKeyStyle.Render("VS Code"), detailValStyle.Render(info.VSCodiumHint))
	fmt.Fprintf(&b, "%s\n%s\n", detailKeyStyle.Render("Cursor"), detailValStyle.Render(info.CursorHint))
	for _, n := range info.Notes {
		if n != "" {
			fmt.Fprintf(&b, "\n%s\n", warningStyle.Render(n))
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(b.String())
}

func renderSpotlightPanel(m *Model, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Spotlight forwards"))
	if len(m.spotForwards) == 0 {
		b.WriteString(mutedStyle.Render("No active forwards. Press n to add (remote port).\n"))
	} else {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render("j/k select · n new · x remove forward"))
		for i, f := range m.spotForwards {
			if f == nil {
				continue
			}
			line := fmt.Sprintf("%s  local:%d remote:%d  %s", f.ID, f.LocalPort, f.RemotePort, f.State)
			if i == m.spotSel {
				fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render("› "+line))
			} else {
				fmt.Fprintf(&b, "%s\n", line)
			}
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(b.String())
}

func renderSyncPanel(m *Model, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Volume sync"))
	if len(m.syncRows) == 0 {
		b.WriteString(mutedStyle.Render("No sync sessions. Press n to start (local path + direction).\n"))
	} else {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render("j/k select · n new · x stop · p pause · r resume"))
		for i, r := range m.syncRows {
			line := fmt.Sprintf("%s  %s  %-12s  %s → %s", r.SessionID, r.Status, r.Direction, r.LocalPath, r.WorkspaceID)
			if i == m.syncSel {
				fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render("› "+truncate(line, 72)))
			} else {
				fmt.Fprintf(&b, "%s\n", truncate(line, 72))
			}
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(b.String())
}

func renderWorkspaceDetail(ws *workspace.Workspace, width int) string {
	if ws == nil {
		return ""
	}
	keyw := 18
	row := func(k, v string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			detailKeyStyle.Width(keyw).Render(k),
			detailValStyle.Width(max(width-keyw-4, 20)).Render(v),
		)
	}
	ports := "(none)"
	if len(ws.TunnelPorts) > 0 {
		parts := make([]string, len(ws.TunnelPorts))
		for i, p := range ws.TunnelPorts {
			parts[i] = fmt.Sprintf("%d", p)
		}
		ports = strings.Join(parts, ", ")
	}
	b := strings.Builder{}
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render(ws.WorkspaceName))
	fmt.Fprintf(&b, "%s\n", row("ID", ws.ID))
	fmt.Fprintf(&b, "%s\n", row("State", string(ws.State)))
	fmt.Fprintf(&b, "%s\n", row("Backend", ws.Backend))
	fmt.Fprintf(&b, "%s\n", row("Guest IP", ws.GuestIP))
	fmt.Fprintf(&b, "%s\n", row("Repo", truncate(ws.Repo, 60)))
	fmt.Fprintf(&b, "%s\n", row("Ref", ws.Ref))
	fmt.Fprintf(&b, "%s\n", row("Root path", truncate(ws.RootPath, 60)))
	fmt.Fprintf(&b, "%s\n", row("Ports", ports))
	created := "—"
	if !ws.CreatedAt.IsZero() {
		created = ws.CreatedAt.UTC().Format(time.RFC3339)
	}
	updated := "—"
	if !ws.UpdatedAt.IsZero() {
		updated = ws.UpdatedAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "%s\n", row("Created", created))
	fmt.Fprintf(&b, "%s\n", row("Updated", updated))
	return b.String()
}
