package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

const headerLines = 2
const footerLines = 3

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

// Run starts the interactive TUI until the user quits.
func Run() error {
	p := tea.NewProgram(NewModel(), tea.WithAltScreen())
	_, err := p.Run()
	return err
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
}

// NewModel constructs an initial model (connection opens during Init).
func NewModel() Model {
	delegate := styledDelegate()
	l := newWorkspaceList(delegate, 80, 20)
	return Model{
		list:       l,
		statusLine: "connecting…",
	}
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

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.listWidth = max(msg.Width-4, 24)
		m.listHeight = max(msg.Height-headerLines-footerLines-4, 6)
		m.list.SetSize(m.listWidth, m.listHeight)
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
		return m, nil

	case workspacesErrMsg:
		m.daemonOK = false
		m.statusLine = msg.err.Error()
		return m, nil

	case refreshTickMsg:
		if m.quitting {
			return m, nil
		}
		if m.conn != nil && m.detail == nil && !m.confirmDelete {
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
		return m, m.refreshCmd()

	case tea.KeyMsg:
		return m.handleKey(msg)
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
	case "esc":
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
		if msg.String() == "enter" {
			m.detail = nil
			return m, nil
		}
		id := m.detail.ID
		switch msg.String() {
		case "s":
			return m, m.rpcVoidCmd("workspace.start", id)
		case "x":
			return m, m.rpcVoidCmd("workspace.stop", id)
		case "d":
			m.confirmDelete = true
			m.pendingDeleteID = id
			return m, nil
		}
		return m, nil
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
		id := wi.w.ID
		switch msg.String() {
		case "s":
			return m, m.rpcVoidCmd("workspace.start", id)
		case "x":
			return m, m.rpcVoidCmd("workspace.stop", id)
		case "d":
			m.confirmDelete = true
			m.pendingDeleteID = id
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
	if m.detail != nil {
		body = renderWorkspaceDetail(m.detail, m.listWidth)
	} else {
		body = m.list.View()
	}

	help := mutedStyle.Render(
		"q quit · enter detail/back · esc back · r refresh · s start · x stop · d delete",
	)

	confirm := ""
	if m.confirmDelete {
		confirm = warningStyle.Render(fmt.Sprintf("Delete workspace %s? [y/N]", truncate(m.pendingDeleteID, 36)))
	}

	footer := lipgloss.JoinVertical(lipgloss.Left, confirm, help)
	box := lipgloss.NewStyle().
		MaxWidth(m.width).
		Padding(0, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			headerTop,
			headerBottom,
			"",
			body,
			"",
			footer,
		))

	return box
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
