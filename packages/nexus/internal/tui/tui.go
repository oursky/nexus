package tui

import (
	"encoding/json"
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

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)


type panelKind int

const (
	panelNone panelKind = iota
	panelConnect
	panelSpotlight
	panelSync
)

const (
	promptNone            = ""
	promptSpotPort        = "spot_port"
	promptSidebarSpotPort = "sidebar_spot_port"
	promptSyncLocal       = "sync_local"
	promptSyncDir         = "sync_dir"
	promptForkChild       = "fork_child"
)

// layoutPane identifies which pane contains a given screen column (for mouse
// hit-testing).
type layoutPane int

const (
	layoutPaneLeft   layoutPane = iota
	layoutPaneCenter layoutPane = iota
	layoutPaneRight  layoutPane = iota
	layoutPaneNone   layoutPane = iota
)

type syncRow struct {
	SessionID   string
	WorkspaceID string
	LocalPath   string
	Status      string
	Direction   string
}

type connReadyMsg struct {
	mux *rpc.MuxConn
}

type connErrMsg struct {
	err error
}

type workspacesMsg struct {
	workspaces   []workspace.Workspace
	projectsByID map[string]string // project ID → name; may be nil
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

// sidebarSpotMsg delivers spotlight.list results for the three-pane sidebar.
type sidebarSpotMsg struct {
	wsID     string
	forwards []*spotlight.Forward
	err      error
}

// sidebarSpotTickMsg fires every 3s to refresh the spotlight sidebar.
type sidebarSpotTickMsg struct{}

// Model is the root Bubble Tea model for the Nexus TUI.
type Model struct {
	list   list.Model
	mux    *rpc.MuxConn
	detail *workspace.Workspace

	// Split-pane PTY state (right pane; active when width >= 100).
	ptyPane    *PtyPane
	ptyWsID    string              // workspace ID of the active PTY (or "")
	ptyFocused bool               // true when right pane has keyboard focus
	ptyDataCh  <-chan json.RawMessage // open pty.data subscription
	cancelPTY  func()             // unsubscribes and closes ptyDataCh
	daemonOK        bool
	quitting        bool
	statusLine      string
	confirmDelete   bool
	pendingDeleteID string
	width           int
	height          int
	listWidth       int
	listHeight      int

	projectsByID map[string]string // project ID → name (refreshed with workspace list)

	panel        panelKind
	showHelp     bool
	spotForwards []*spotlight.Forward
	spotSel      int
	syncRows     []syncRow
	syncSel      int

	prompt      string
	promptInput textinput.Model

	createMode bool
	createStep int
	nameTI     textinput.Model
	repoTI     textinput.Model
	refTI      textinput.Model

	pendingSyncPath string

	// no-profile view — shown on first run when no profile exists
	showNoProfile   bool
	noProfileSel    int    // 0=start local, 1=connect remote
	noProfileBusy   bool   // spinning while starting daemon
	noProfileChecking bool // waiting for local daemon check result
	noProfileErr    string
	noProfileSpinIdx int
	localPort       int // port to use for local daemon operations

	// connect wizard — shown when no daemon profile is configured
	showWizard   bool
	wizardStep   int // 0=host, 1=port, 2=sshkey
	wizardHostTI textinput.Model
	wizardPortTI textinput.Model
	wizardKeyTI  textinput.Model
	wizardErr    string
	wizardBusy   bool

	// session tabs — workspaces attached at least once this TUI session
	tabs           []string // workspace IDs in order
	activeTabWS    string   // last attached workspace ID
	autoAttach     bool     // --auto-attach flag
	autoAttachDone bool     // prevent multiple auto-attaches

	// Three-pane spotlight sidebar (visible when width >= 110).
	sidebarFocused bool
	sidebarSel     int
	sidebarFwds    []*spotlight.Forward

	// Pane x-boundaries for mouse hit-testing (0-indexed, inner coordinates,
	// i.e. after removing the 2-column outer padding).
	// leftPaneRight  = last inner column of the left pane (= leftW - 1)
	// centerPaneRight = last inner column of the center pane (= leftW + centerW)
	leftPaneRight   int
	centerPaneRight int

	// mouseSupportEnabled is set to true after the first WindowSizeMsg so that
	// mouse coordinates are valid before we start hit-testing.
	mouseSupportEnabled bool
}

// tabBarOffset returns the number of extra lines consumed by the tab bar
// (1 when tabs are present, 0 otherwise).
func (m Model) tabBarOffset() int {
	if len(m.tabs) == 0 {
		return 0
	}
	return 1
}

// isSplitMode reports whether the terminal is wide enough for the split-pane
// layout (left workspace list + right interactive PTY).
func (m Model) isSplitMode() bool {
	return m.width >= 90
}

// isThreePaneMode reports whether the terminal is wide enough for the
// three-pane layout (workspace list | PTY | spotlight sidebar).
func (m Model) isThreePaneMode() bool {
	return m.width >= 110
}

// paneDimensions returns (leftW, centerW, rightW) for the current layout.
//
//   - Three-pane (>= 110 cols): leftW=22%, centerW=55%-ish, rightW=23%.
//   - Two-pane   (>= 90 cols):  leftW=28%, centerW=72%-ish, rightW=0.
//   - Single-pane:              leftW=centerW=full inner, rightW=0.
//
// The widths already account for the separator column(s) so that
// leftW + centerW + rightW + numSeparators == inner.
func (m Model) paneDimensions() (leftW, centerW, rightW int) {
	inner := max(m.width-4, 24)
	if m.isThreePaneMode() {
		leftW = int(float64(inner) * 0.22)
		rightW = int(float64(inner) * 0.23)
		centerW = max(inner-leftW-rightW-2, 20) // 2 separator columns
		return
	}
	if m.isSplitMode() {
		leftW = int(float64(inner) * 0.28)
		centerW = max(inner-leftW-1, 20) // 1 separator column
		rightW = 0
		return
	}
	leftW = inner
	centerW = inner
	rightW = 0
	return
}

// leftPaneWidth returns the width the workspace list should occupy.
func (m Model) leftPaneWidth() int {
	leftW, _, _ := m.paneDimensions()
	return leftW
}

// ptyPaneDimensions returns the (cols, rows) the PTY center pane should use.
func (m Model) ptyPaneDimensions() (cols, rows int) {
	_, centerW, _ := m.paneDimensions()
	cols = centerW
	rows = max(m.height-7-2*m.tabBarOffset(), 4)
	return cols, rows
}

// paneAtX returns which layout pane contains terminal column x.
// x is an absolute terminal column (0-indexed).
func (m Model) paneAtX(x int) layoutPane {
	if !m.isSplitMode() {
		return layoutPaneNone
	}
	// Subtract the 2-column outer padding to get an inner-relative index.
	adj := x - 2
	if adj < 0 {
		return layoutPaneNone
	}
	if adj <= m.leftPaneRight {
		return layoutPaneLeft
	}
	if !m.isThreePaneMode() {
		return layoutPaneCenter
	}
	if adj <= m.centerPaneRight {
		return layoutPaneCenter
	}
	return layoutPaneRight
}

// selectedWorkspace returns the workspace currently highlighted in the list,
// or nil if there is none.
func (m Model) selectedWorkspace() *workspace.Workspace {
	it := m.list.SelectedItem()
	if it == nil {
		return nil
	}
	wi, ok := it.(workspaceItem)
	if !ok {
		return nil
	}
	return &wi.w
}

// maybeSwitchPTY opens or closes the right-pane PTY session based on the
// currently selected workspace. It is a no-op when the selection or the
// running state hasn't changed. Must be called from Update (not View).
func (m Model) maybeSwitchPTY() (Model, tea.Cmd) {
	if !m.isSplitMode() {
		return m, nil
	}

	// Determine which workspace we want a PTY for.
	selWS := m.selectedWorkspace()
	var desiredWsID string
	if selWS != nil && selWS.State == workspace.StateRunning {
		desiredWsID = selWS.ID
	}

	// No change needed.
	if m.ptyWsID == desiredWsID {
		return m, nil
	}

	// Close the old PTY subscription (channel closes → listener goroutine exits).
	if m.cancelPTY != nil {
		m.cancelPTY()
		m.cancelPTY = nil
	}
	// Ask the daemon to close the old session (best-effort, non-blocking).
	if m.mux != nil && m.ptyPane != nil && m.ptyPane.sessionID != "" {
		mux := m.mux
		sid := m.ptyPane.sessionID
		go func() { _ = mux.Send("pty.close", map[string]any{"sessionId": sid}) }()
	}
	m.ptyPane = nil
	m.ptyDataCh = nil
	m.ptyFocused = false
	m.ptyWsID = desiredWsID // set now to prevent duplicate opens on the next tick

	if desiredWsID == "" || m.mux == nil {
		return m, nil
	}
	cols, rows := m.ptyPaneDimensions()
	return m, openPTYCmd(m.mux, desiredWsID, cols, rows)
}

// updateListSize recalculates listHeight from terminal dimensions and calls
// SetSize on the bubbles list. Must be called whenever m.height or m.tabs
// changes.
//
// Layout: 2 header lines + 2*tabBarOffset tab rows + 2 spacers + 3 footer
// lines = 7 + 2*tabBarOffset lines of fixed overhead. The list occupies the
// remaining body height.
func (m *Model) updateListSize() {
	if m.height == 0 {
		return
	}
	h := max(m.height-7-2*m.tabBarOffset(), 6)
	m.listHeight = h
	m.list.SetSize(m.listWidth, h)
}

func (m Model) blocksOverlayInput() bool {
	if m.showNoProfile {
		return true
	}
	if m.showWizard {
		return true
	}
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
	// Port is the local daemon port for the no-profile check/start flow.
	// 0 means use the compiled-in default (7777 prod, 7778 dev).
	Port int
}

// Run starts the interactive TUI until the user quits.
func Run(opts ...Options) error {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	p := tea.NewProgram(NewModel(o), tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := p.Run()
	return err
}

// NewModel constructs an initial model (connection opens during Init).
func NewModel(opts ...Options) Model {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	l := newWorkspaceList(80, 20)
	l.SetFilteringEnabled(true)

	pi := textinput.New()
	pi.Prompt = "> "
	pi.CharLimit = 512

	nt, rt, rf := newCreateInputs()

	// Restore session state from disk.
	ss := loadSessionState()

	lp := o.Port
	if lp == 0 {
		lp = 7777
	}

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
		localPort:   lp,
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
		mux, err := rpc.EnsureMux()
		if err != nil {
			return connErrMsg{err: err}
		}
		return connReadyMsg{mux: mux}
	}
}

func (m Model) refreshCmd() tea.Cmd {
	mux := m.mux
	if mux == nil {
		return nil
	}
	return func() tea.Msg {
		// Fetch workspace list and project list in parallel.
		type wsResult struct {
			ws  []workspace.Workspace
			err error
		}
		type projResult struct {
			byID map[string]string
		}
		wsCh := make(chan wsResult, 1)
		projCh := make(chan projResult, 1)
		go func() {
			ws, err := fetchWorkspaces(mux)
			wsCh <- wsResult{ws, err}
		}()
		go func() {
			projCh <- projResult{fetchProjectsByID(mux)}
		}()
		wr := <-wsCh
		if wr.err != nil {
			return workspacesErrMsg{err: wr.err}
		}
		pr := <-projCh
		return workspacesMsg{workspaces: wr.ws, projectsByID: pr.byID}
	}
}

func fetchWorkspaces(mux *rpc.MuxConn) ([]workspace.Workspace, error) {
	var result struct {
		Workspaces []*workspace.Workspace `json:"workspaces"`
	}
	if err := mux.Call("workspace.list", map[string]any{}, &result); err != nil {
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

// fetchProjectsByID calls project.list and returns a map of project ID → name.
// Returns nil on error so callers can treat it as best-effort.
func fetchProjectsByID(mux *rpc.MuxConn) map[string]string {
	var result struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := mux.Call("project.list", map[string]any{}, &result); err != nil {
		return nil
	}
	m := make(map[string]string, len(result.Projects))
	for _, p := range result.Projects {
		m[p.ID] = p.Name
	}
	return m
}

func (m Model) loadDetailCmd(id string) tea.Cmd {
	mux := m.mux
	if mux == nil {
		return nil
	}
	return func() tea.Msg {
		var result struct {
			Workspace *workspace.Workspace `json:"workspace"`
		}
		if err := mux.Call("workspace.info", map[string]any{"id": id}, &result); err != nil {
			return detailErrMsg{err: err}
		}
		if result.Workspace == nil {
			return detailErrMsg{err: fmt.Errorf("workspace.info: empty workspace")}
		}
		return detailLoadedMsg{ws: *result.Workspace}
	}
}

func (m Model) rpcVoidCmd(method string, id string) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := mux.Call(method, map[string]any{"id": id}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) loadSpotlightsCmd(wsID string) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return spotlightsDataMsg{err: fmt.Errorf("not connected")}
		}
		var result struct {
			Forwards []*spotlight.Forward `json:"forwards"`
		}
		if err := mux.Call("spotlight.list", map[string]any{"workspaceId": wsID}, &result); err != nil {
			return spotlightsDataMsg{err: err}
		}
		return spotlightsDataMsg{forwards: result.Forwards}
	}
}

// loadSidebarSpotCmd fetches spotlight.list for the currently selected
// workspace and delivers a sidebarSpotMsg.
func (m Model) loadSidebarSpotCmd() tea.Cmd {
	ws := m.selectedWorkspace()
	if ws == nil || m.mux == nil {
		return nil
	}
	wsID := ws.ID
	mux := m.mux
	return func() tea.Msg {
		var result struct {
			Forwards []*spotlight.Forward `json:"forwards"`
		}
		if err := mux.Call("spotlight.list", map[string]any{"workspaceId": wsID}, &result); err != nil {
			return sidebarSpotMsg{wsID: wsID, err: err}
		}
		return sidebarSpotMsg{wsID: wsID, forwards: result.Forwards}
	}
}

// sidebarSpotTickCmd schedules the next sidebar spotlight refresh (3s).
func sidebarSpotTickCmd() tea.Cmd {
	return tea.Every(3*time.Second, func(time.Time) tea.Msg {
		return sidebarSpotTickMsg{}
	})
}

func (m Model) loadSyncListCmd(wsID string) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
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
		if err := mux.Call("workspace.sync-list", map[string]any{"workspaceId": wsID}, &result); err != nil {
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
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
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
		if err := mux.Call("spotlight.start", map[string]any{"workspaceId": wsID, "spec": spec}, &result); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) removeForwardCmd(wsID, forwardID string) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.ports.remove", map[string]any{"workspaceId": wsID, "forwardId": forwardID}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) syncStartCmd(wsID, localPath, direction string) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.sync-start", map[string]any{
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
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.sync-stop", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) syncPauseCmd(sessionID string) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.sync-pause", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) syncResumeCmd(sessionID string) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return mutationDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.sync-resume", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return mutationDoneMsg{err: err}
		}
		return mutationDoneMsg{err: nil}
	}
}

func (m Model) forkCmd(parentID, childName string) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return forkDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.fork", map[string]any{
			"id":                 parentID,
			"childWorkspaceName": childName,
		}, nil); err != nil {
			return forkDoneMsg{err: err}
		}
		return forkDoneMsg{err: nil}
	}
}

func (m Model) createWorkspaceCmd(spec workspace.CreateSpec) tea.Cmd {
	mux := m.mux
	return func() tea.Msg {
		if mux == nil {
			return createDoneMsg{err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.create", map[string]any{"spec": spec}, nil); err != nil {
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
		m.mouseSupportEnabled = true
		m.listWidth = m.leftPaneWidth()
		m.updateListSize()

		// Update pane x-boundaries for mouse hit-testing.
		leftW, centerW, _ := m.paneDimensions()
		m.leftPaneRight = leftW - 1
		m.centerPaneRight = leftW + centerW // leftW + sep(1) + centerW - 1

		// Resize the PTY pane if visible and send pty.resize to daemon.
		var resizeCmd tea.Cmd
		if m.ptyPane != nil && m.mux != nil {
			cols, rows := m.ptyPaneDimensions()
			m.ptyPane.Resize(cols, rows)
			resizeCmd = m.ptyPane.resizeCmd(m.mux)
		}
		// Re-evaluate PTY visibility after size change.
		m, ptyCmd := m.maybeSwitchPTY()
		return m, tea.Batch(resizeCmd, ptyCmd)

	case connReadyMsg:
		m.mux = msg.mux
		m.daemonOK = true
		m.statusLine = ""
		return m, tea.Batch(
			m.refreshCmd(),
			tickRefresh(),
			sidebarSpotTickCmd(),
		)

	case connErrMsg:
		m.daemonOK = false
		errStr := msg.err.Error()
		if strings.Contains(errStr, "no daemon profile configured") {
			// First-run: no profile at all — check if a local daemon is already
			// running before showing the no-profile menu.
			m.showNoProfile = true
			m.noProfileChecking = true
			m.noProfileSel = 0
			m.noProfileErr = ""
			return m, tea.Batch(checkLocalDaemonCmd(m.localPort), noProfileSpinTick())
		}
		if strings.Contains(errStr, "connect to local daemon") && strings.Contains(errStr, "connection refused") {
			// A localhost profile exists but the daemon isn't running.
			// Treat exactly like no-profile: show the start/connect menu.
			m.showNoProfile = true
			m.noProfileChecking = false
			m.noProfileBusy = false
			m.noProfileSel = 0
			m.noProfileErr = ""
			return m, nil
		}
		m.statusLine = errStr
		return m, nil

	case localCheckMsg:
		m.noProfileChecking = false
		if msg.alive {
			// Daemon is already running locally — auto-save profile and connect.
			m.statusLine = "connecting…"
			return m, saveLocalProfileCmd(m.localPort)
		}
		// Nothing running — show the menu.
		return m, nil

	case daemonStartDoneMsg:
		m.noProfileBusy = false
		if msg.err != nil {
			m.noProfileErr = msg.err.Error()
			return m, nil
		}
		// Daemon started — save profile and connect.
		m.statusLine = "connecting…"
		return m, saveLocalProfileCmd(m.localPort)

	case noProfileSpinTickMsg:
		m.noProfileSpinIdx++
		if m.noProfileBusy || m.noProfileChecking {
			return m, noProfileSpinTick()
		}
		return m, nil

	case wizardSavedMsg:
		m.showWizard = false
		m.showNoProfile = false
		m.noProfileBusy = false
		m.wizardBusy = false
		m.statusLine = "connecting…"
		return m, openDaemonConn()

	case wizardErrMsg:
		m.wizardBusy = false
		m.wizardErr = msg.err.Error()
		if m.showNoProfile {
			// Error occurred during local profile save triggered from no-profile flow.
			m.noProfileBusy = false
			m.noProfileErr = msg.err.Error()
		}
		return m, nil

	case workspacesMsg:
		m.daemonOK = true
		if msg.projectsByID != nil {
			m.projectsByID = msg.projectsByID
		}
		sel := selectedWorkspaceID(m.list.SelectedItem())
		m.list.SetItems(workspacesToItems(msg.workspaces, m.projectsByID))
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
		m, ptyCmd := m.maybeSwitchPTY()
		return m, ptyCmd

	// PTY split-pane messages.
	case ptyOpenedMsg:
		if msg.wsID != m.ptyWsID {
			// Selection changed while pty.create was in-flight; discard.
			msg.cancelFn()
			if m.mux != nil {
				mux := m.mux
				sid := msg.sessionID
				go func() { _ = mux.Send("pty.close", map[string]any{"sessionId": sid}) }()
			}
			return m, nil
		}
		cols, rows := m.ptyPaneDimensions()
		m.ptyPane = NewPtyPane(msg.wsID, msg.sessionID, cols, rows)
		m.ptyDataCh = msg.dataCh
		m.cancelPTY = msg.cancelFn
		return m, listenPTYCmd(msg.dataCh, msg.sessionID)

	case ptyDataMsg:
		if m.ptyPane != nil && msg.sessionID == m.ptyPane.sessionID {
			m.ptyPane.Write(msg.data)
		}
		if m.ptyDataCh != nil {
			return m, listenPTYCmd(m.ptyDataCh, msg.sessionID)
		}
		return m, nil

	case ptyClosedMsg:
		if m.ptyPane != nil && msg.sessionID == m.ptyPane.sessionID {
			m.ptyPane = nil
			m.ptyWsID = ""
			m.cancelPTY = nil
			m.ptyDataCh = nil
			m.ptyFocused = false
		}
		return m, nil

	case ptyErrMsg:
		if msg.err != nil {
			m.statusLine = "PTY: " + msg.err.Error()
		}
		m.ptyWsID = "" // allow retry on next refresh
		return m, nil

	case workspacesErrMsg:
		m.daemonOK = false
		m.statusLine = msg.err.Error()
		return m, nil

	case refreshTickMsg:
		if m.quitting {
			return m, nil
		}
		if m.mux != nil && !m.blocksOverlayInput() {
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
		cmds := []tea.Cmd{m.refreshCmd()}
		if m.isThreePaneMode() && m.mux != nil {
			cmds = append(cmds, m.loadSidebarSpotCmd())
		}
		return m, tea.Batch(cmds...)

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

	case sidebarSpotMsg:
		if msg.err == nil {
			m.sidebarFwds = msg.forwards
			if m.sidebarSel >= len(m.sidebarFwds) {
				m.sidebarSel = max(0, len(m.sidebarFwds)-1)
			}
		}
		return m, nil

	case sidebarSpotTickMsg:
		if m.quitting {
			return m, nil
		}
		var cmds []tea.Cmd
		if m.isThreePaneMode() && m.mux != nil && !m.blocksOverlayInput() {
			cmds = append(cmds, m.loadSidebarSpotCmd())
		}
		cmds = append(cmds, sidebarSpotTickCmd())
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.showWizard {
		var cmd tea.Cmd
		switch m.wizardStep {
		case 0:
			m.wizardHostTI, cmd = m.wizardHostTI.Update(msg)
		case 1:
			m.wizardPortTI, cmd = m.wizardPortTI.Update(msg)
		case 2:
			m.wizardKeyTI, cmd = m.wizardKeyTI.Update(msg)
		}
		return m, cmd
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

// sendMouseToPTY encodes a BubbleTea mouse event as a VT200 mouse escape
// sequence and forwards it to the active PTY via the pty.write RPC.
func (m Model) sendMouseToPTY(msg tea.MouseMsg) tea.Cmd {
	if m.ptyPane == nil || m.mux == nil {
		return nil
	}
	leftW, _, _ := m.paneDimensions()
	// PTY view origin (absolute terminal coordinates, 0-indexed):
	//   X: outer padding (2) + left pane (leftW) + separator (1)
	//   Y: 2 header lines + 1 blank line + 2*tabBarOffset tab lines
	ptyX := 2 + leftW + 1
	ptyY := 3 + 2*m.tabBarOffset()

	// PTY-relative, 1-indexed.
	px := msg.X - ptyX + 1
	py := msg.Y - ptyY + 1
	if px < 1 {
		px = 1
	}
	if py < 1 {
		py = 1
	}
	// VT200 X10 mouse: coordinates encoded as char+32, max 223.
	if px > 223 {
		px = 223
	}
	if py > 223 {
		py = 223
	}

	var btn int
	switch msg.Button {
	case tea.MouseButtonLeft:
		btn = 0
		if msg.Action == tea.MouseActionRelease {
			btn = 3
		}
	case tea.MouseButtonMiddle:
		btn = 1
	case tea.MouseButtonRight:
		btn = 2
	case tea.MouseButtonWheelUp:
		btn = 64
	case tea.MouseButtonWheelDown:
		btn = 65
	default:
		return nil
	}

	data := fmt.Sprintf("\x1b[M%c%c%c", rune(btn+32), rune(px+32), rune(py+32))
	sessionID := m.ptyPane.sessionID
	mux := m.mux
	return func() tea.Msg {
		_ = mux.Send("pty.write", map[string]any{
			"sessionId": sessionID,
			"data":      data,
		})
		return nil
	}
}

// handleMouse handles tea.MouseMsg events for click-to-focus, scroll, and PTY
// mouse forwarding.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.blocksOverlayInput() || !m.mouseSupportEnabled {
		return m, nil
	}

	pane := m.paneAtX(msg.X)

	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		switch pane {
		case layoutPaneLeft:
			var listCmd tea.Cmd
			if msg.Button == tea.MouseButtonWheelUp {
				m.list, listCmd = m.list.Update(tea.KeyMsg{Type: tea.KeyUp})
			} else {
				m.list, listCmd = m.list.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			m, ptyCmd := m.maybeSwitchPTY()
			return m, tea.Batch(listCmd, ptyCmd)
		case layoutPaneCenter:
			if m.ptyPane != nil {
				return m, m.sendMouseToPTY(msg)
			}
		case layoutPaneRight:
			if msg.Button == tea.MouseButtonWheelUp {
				if m.sidebarSel > 0 {
					m.sidebarSel--
				}
			} else {
				if m.sidebarSel < len(m.sidebarFwds)-1 {
					m.sidebarSel++
				}
			}
		}

	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionRelease {
			if pane == layoutPaneCenter && m.ptyPane != nil {
				return m, m.sendMouseToPTY(msg)
			}
			return m, nil
		}
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		switch pane {
		case layoutPaneLeft:
			m.ptyFocused = false
			m.sidebarFocused = false
		case layoutPaneCenter:
			if !m.ptyFocused {
				m.ptyFocused = true
				m.sidebarFocused = false
			}
			if m.ptyPane != nil {
				return m, m.sendMouseToPTY(msg)
			}
		case layoutPaneRight:
			m.sidebarFocused = true
			m.ptyFocused = false
			// Best-effort click-to-select: sidebar content starts after 2 header
			// rows (title + separator). ptyY gives the body start.
			bodyY := 3 + 2*m.tabBarOffset()
			relY := msg.Y - bodyY - 2 // -2 for "SPOTLIGHT\n────\n"
			if relY >= 0 && relY < len(m.sidebarFwds) {
				m.sidebarSel = relY
			}
		}

	default:
		if pane == layoutPaneCenter && m.ptyPane != nil {
			return m, m.sendMouseToPTY(msg)
		}
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// When the spotlight sidebar has focus, route keys there.
	if m.sidebarFocused && m.isThreePaneMode() && !m.blocksOverlayInput() {
		return m.handleSidebarKeys(msg)
	}

	// When the PTY right pane has focus, forward all keys to it.
	// ctrl+a detaches from the PTY pane; in three-pane mode it moves focus to
	// the spotlight sidebar instead of the left pane.
	if m.ptyFocused && m.ptyPane != nil && !m.blocksOverlayInput() {
		if msg.String() == "ctrl+a" {
			m.ptyFocused = false
			if m.isThreePaneMode() {
				m.sidebarFocused = true
			}
			return m, nil
		}
		// Forward all other keys (including ctrl+c, q) to the PTY.
		return m, m.ptyPane.sendInputCmd(m.mux, msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		if !m.showWizard && !m.showNoProfile {
			m.quitting = true
			if m.cancelPTY != nil {
				m.cancelPTY()
				m.cancelPTY = nil
			}
			if m.mux != nil {
				m.mux.Close()
				m.mux = nil
			}
			return m, tea.Quit
		}
	}

	if m.showNoProfile {
		return m.handleNoProfileKeys(msg)
	}

	if m.showWizard {
		return m.handleWizardKeys(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		if m.cancelPTY != nil {
			m.cancelPTY()
			m.cancelPTY = nil
		}
		if m.mux != nil {
			m.mux.Close()
			m.mux = nil
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
		case promptSidebarSpotPort:
			m.prompt = promptNone
			ws := m.selectedWorkspace()
			if ws == nil {
				return m, nil
			}
			p, err := strconv.Atoi(line)
			if err != nil || p <= 0 || p > 65535 {
				m.statusLine = "invalid port"
				return m, m.loadSidebarSpotCmd()
			}
			return m, m.startSpotlightCmd(ws.ID, p)
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

// handleSidebarKeys handles keyboard input when the spotlight sidebar pane has
// focus (three-pane layout only).
func (m Model) handleSidebarKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "esc":
		m.sidebarFocused = false
		return m, nil
	case "n":
		ws := m.selectedWorkspace()
		if ws == nil {
			return m, nil
		}
		m.prompt = promptSidebarSpotPort
		m.promptInput.Placeholder = "remote port"
		return m, m.promptInput.Focus()
	case "x", "delete":
		ws := m.selectedWorkspace()
		if ws == nil || len(m.sidebarFwds) == 0 {
			return m, nil
		}
		if m.sidebarSel < 0 || m.sidebarSel >= len(m.sidebarFwds) {
			return m, nil
		}
		f := m.sidebarFwds[m.sidebarSel]
		if f == nil {
			return m, nil
		}
		return m, m.removeForwardCmd(ws.ID, f.ID)
	case "up", "k":
		if m.sidebarSel > 0 {
			m.sidebarSel--
		}
		return m, nil
	case "down", "j":
		if m.sidebarSel < len(m.sidebarFwds)-1 {
			m.sidebarSel++
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
	// l key in three-pane mode focuses the spotlight sidebar directly.
	if m.isThreePaneMode() && msg.String() == "l" {
		m.sidebarFocused = true
		m.ptyFocused = false
		return m, nil
	}

	// In split mode, Tab transfers focus: left → PTY → (three-pane: sidebar).
	if m.isSplitMode() && msg.String() == "tab" {
		if m.ptyPane != nil {
			m.ptyFocused = true
		} else if m.isThreePaneMode() {
			m.sidebarFocused = true
		}
		return m, nil
	}

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

	var listCmd tea.Cmd
	m.list, listCmd = m.list.Update(msg)
	// After any navigation, re-evaluate which PTY session to show.
	m, ptyCmd := m.maybeSwitchPTY()
	return m, tea.Batch(listCmd, ptyCmd)
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

// renderFooterHelp returns the single-line footer hint text, responsive to
// terminal width and clipped so it never exceeds innerWidth characters.
func (m Model) renderFooterHelp(innerWidth int) string {
	var raw string
	switch {
	case m.showNoProfile:
		if m.noProfileChecking || m.noProfileBusy {
			raw = "esc quit"
		} else {
			raw = "↑↓ j/k navigate   ↵ select   esc quit"
		}
	case m.showWizard:
		raw = "tab next field   ↵ connect   esc skip (localhost)"
	case m.panel == panelSpotlight:
		raw = "esc/l close   n new port   x remove   j/k navigate"
	case m.panel == panelSync:
		raw = "esc/y close   n new   x stop   p pause   r resume   j/k navigate"
	case m.panel == panelConnect:
		raw = "esc/c close"
	case m.createMode:
		raw = "tab next field   ↵ next/submit   esc cancel"
	case m.detail != nil && m.panel == panelNone && !m.showHelp:
		raw = "q quit   ? help   esc back   r refresh   c connect   l spotlight   y sync   t terminal   f fork   s/x/d"
	case m.sidebarFocused && m.isThreePaneMode():
		raw = "tab→left  n add  x del  j/k navigate  l unfocus"
	case m.ptyFocused && m.ptyPane != nil:
		if m.isThreePaneMode() {
			raw = "ctrl+a→spotlight   t full-screen shell"
		} else {
			raw = "ctrl+a unfocus terminal   t full-screen shell"
		}
	default:
		if m.isThreePaneMode() {
			switch {
			case m.width < 140:
				raw = "q quit   ? help   tab focus PTY   l spotlight   t full-screen"
			default:
				raw = "q quit   ? help   tab focus PTY   l spotlight   t full-screen   ↵ detail   n new   / filter   s start   x stop   d delete"
			}
		} else if m.isSplitMode() {
			switch {
			case m.width < 120:
				raw = "q quit   ? help   tab focus terminal   t full-screen"
			default:
				raw = "q quit   ? help   tab focus terminal   t full-screen   ↵ detail   n new   / filter   s start   x stop   d delete"
			}
		} else {
			switch {
			case m.width < 60:
				raw = "q  ?"
			case m.width < 80:
				raw = "q quit   ? help   ↵ open   esc back"
			default:
				raw = "q quit   ? help   ↵ detail   t terminal   esc back   r refresh   n new   / filter   s start   x stop   d delete   1-9 tab"
			}
		}
	}
	// Reserve 2 chars for the leading "  " prefix; clip to fit inner width.
	clipped := truncate(raw, max(innerWidth-2, 1))
	return mutedStyle.Render("  " + clipped)
}

// View implements tea.Model.
func (m Model) View() string {
	var statusDaemon string
	if m.quitting {
		statusDaemon = mutedStyle.Render("● exiting")
	} else if m.showNoProfile {
		statusDaemon = mutedStyle.Render("● setup")
	} else if m.showWizard {
		statusDaemon = mutedStyle.Render("● setup")
	} else if m.mux != nil && m.daemonOK {
		statusDaemon = statusOkStyle.Render("● connected")
	} else if m.mux != nil && !m.daemonOK {
		statusDaemon = warningStyle.Render("● degraded")
	} else {
		statusDaemon = statusErrStyle.Render("● disconnected")
	}

	headerTop := lipgloss.JoinHorizontal(lipgloss.Left,
		accentStyle.Render("nexus"),
		mutedStyle.Render("  "),
		statusDaemon,
	)
	headerBottom := ""
	if strings.TrimSpace(m.statusLine) != "" {
		headerBottom = mutedStyle.Render(truncate(m.statusLine, max(m.width-4, 40)))
	}

	body := ""
	// Use split-pane layout on wide terminals when no full-width overlay is
	// active (detail view, help, panels, create wizard, etc.).
	useSplit := m.isSplitMode() && !m.blocksOverlayInput() && m.detail == nil &&
		!m.showHelp && !m.createMode
	switch {
	case m.showNoProfile:
		body = renderNoProfile(&m)
	case m.showWizard:
		body = renderWizard(&m)
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
	case useSplit:
		body = m.renderSplitLayout()
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

	innerWidth := max(m.width-4, 20)

	confirm := ""
	if m.confirmDelete {
		confirm = warningStyle.Render(fmt.Sprintf("Delete workspace %s? [y/N]", truncate(m.pendingDeleteID, 36)))
	}

	sep := separatorStyle.Render(strings.Repeat("─", innerWidth))
	footerHelp := m.renderFooterHelp(innerWidth)
	footer := lipgloss.JoinVertical(lipgloss.Left, sep, confirm, footerHelp)

	// Build the vertical stack. Insert the tab bar when present.
	tabBar := m.renderTabBar()
	tabBarH := 0
	rows := []string{headerTop, headerBottom}
	if tabBar != "" {
		tabBarH = 2
		rows = append(rows, tabSepStyle.Render(strings.Repeat("─", innerWidth)))
		rows = append(rows, tabBar)
	}

	// Sticky footer: pad body to exactly fill the remaining height so that
	// the footer always appears at the very bottom of the screen.
	// Overhead: 2 header + tabBarH + 2 spacers (blank lines around body) + 3 footer = 7 + tabBarH.
	if m.height > 0 {
		contentH := m.height - 7 - tabBarH
		if contentH < 1 {
			contentH = 1
		}
		body = lipgloss.NewStyle().Height(contentH).Render(body)
	}

	rows = append(rows, "", body, "", footer)

	box := lipgloss.NewStyle().
		MaxWidth(m.width).
		Padding(0, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, rows...))

	return box
}

// renderSplitLayout renders the split-pane view.
//
// Two-pane  (90–109 cols): workspace list (28%) | PTY (72%).
// Three-pane (≥110 cols):  workspace list (22%) | PTY (55%) | spotlight (23%).
func (m Model) renderSplitLayout() string {
	leftW, centerW, rightW := m.paneDimensions()
	bodyH := max(m.height-7-2*m.tabBarOffset(), 4)

	// Left pane: workspace list.
	leftContent := m.list.View()
	leftPane := lipgloss.NewStyle().Width(leftW).Height(bodyH).Render(leftContent)

	// First separator: accent when PTY pane is focused.
	sep1Color := colorBorder
	if m.ptyFocused {
		sep1Color = colorAccent
	}
	sep1 := lipgloss.NewStyle().Foreground(sep1Color).Render("│")

	// Center pane content: PTY or workspace info.
	var centerContent string
	if m.ptyPane != nil {
		centerContent = m.ptyPane.Render()
	} else {
		selWS := m.selectedWorkspace()
		switch {
		case selWS != nil && selWS.State == workspace.StateRunning:
			centerContent = mutedStyle.Render("Opening terminal…")
		case selWS != nil:
			centerContent = renderWorkspaceDetail(selWS, centerW)
		default:
			centerContent = mutedStyle.Render("Select a workspace")
		}
	}

	if !m.isThreePaneMode() {
		// Two-pane layout: center pane has a left border rendered as part of
		// the pane box (matches the original layout).
		centerBorderColor := colorBorder
		if m.ptyFocused {
			centerBorderColor = colorAccent
		}
		centerPane := lipgloss.NewStyle().
			Width(centerW).
			Height(bodyH).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(centerBorderColor).
			Render(centerContent)
		return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, sep1, centerPane)
	}

	// Three-pane layout.
	centerPane := lipgloss.NewStyle().Width(centerW).Height(bodyH).Render(centerContent)

	// Second separator: accent when spotlight sidebar is focused.
	sep2Color := colorBorder
	if m.sidebarFocused {
		sep2Color = colorAccent
	}
	sep2 := lipgloss.NewStyle().Foreground(sep2Color).Render("│")

	spotlightContent := renderSpotlightSidebar(&m, rightW, bodyH)
	rightPane := lipgloss.NewStyle().Width(rightW).Height(bodyH).Render(spotlightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, sep1, centerPane, sep2, rightPane)
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
