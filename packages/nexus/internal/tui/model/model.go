package model

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/pty"
)

// ViewRenderer is implemented by view types that can render themselves.
// This interface breaks the circular import between model and views packages.
type ViewRenderer interface {
	Render(m *AppModel) string
}

// AppModel is the root BubbleTea model for the Nexus TUI.
type AppModel struct {
	// Workspace management
	mux           *rpc.MuxConn
	workspaces    []Workspace
	workspaceList list.Model
	selectedWS    string

	// Tab management
	tabs      []PTYSession
	activeTab int

	// PTY pane (currently active terminal)
	ptyPane *pty.PtyPane

	// View state
	currentView View
	headerWidth int
	footerWidth int

	// Search/filter
	searchInput textinput.Model
	filter      string

	// Notifications
	toasts []Toast

	// Modal
	modal ModalState

	// Spotlight data
	forwards     []messages.PortForwardItem
	syncSessions []messages.SyncSessionItem

	// Create form state
	createForm CreateForm

	// Dimensions
	width  int
	height int

	// View renderer (set by tui.go to break model↔views cycle)
	renderer ViewRenderer

	// Update router function (set by tui.go to break model↔update cycle)
	updateRouter UpdateFunc

	// Poll command factory (set by tui.go to break model↔commands cycle)
	pollCmd func() tea.Cmd
}

// View represents the current active view.
type View int

const (
	ViewDashboard View = iota
	ViewOnramp
	ViewCreate
	ViewDetail
	ViewSpotlight
	ViewSync
	ViewHelp
	ViewConnect
)

// Workspace represents a workspace in the UI.
type Workspace struct {
	ID      string
	Name    string
	Repo    string
	Path    string
	State   string
	Project string
}

// Toast represents a notification toast.
type Toast struct {
	Message string
	Kind    messages.ToastKind
}

// ModalState represents the current modal state.
type ModalState struct {
	Active bool
	Type   string
	Title  string
	Body   string
	Action string
}

// Shortcut represents a keyboard shortcut with its description.
type Shortcut struct {
	Key         string
	Description string
}

// Tab represents a terminal tab in the detail view.
type Tab struct {
	ID    string
	Title string
	Type  string
}

// PortForward represents a port forwarding configuration.
type PortForward struct {
	LocalPort  int
	RemotePort int
	Protocol   string
}

// SpotlightItem represents an item in the spotlight search palette.
type SpotlightItem struct {
	ID          string
	Label       string
	Description string
	Type        string
}

// SyncFile represents a file in the sync view.
type SyncFile struct {
	Path   string
	Status string
	Size   int64
}

// CreateForm holds the state for the workspace creation form.
type CreateForm struct {
	Name  string
	Repo  string
	Ref   string
	Focus int // 0=name, 1=repo, 2=ref
	Err   string
}

// NewAppModel creates a new AppModel.
func NewAppModel(mux *rpc.MuxConn) *AppModel {
	// Initialize workspace list with design tokens
	delegate := list.NewDefaultDelegate()
	t := design.GetTheme()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(t.Colors.Text).
		BorderLeftForeground(t.Colors.Accent)
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.
		Foreground(t.Colors.Text)
	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.
		Foreground(t.Colors.TextMuted)
	workspaceList := list.New(nil, delegate, 80, 24)
	workspaceList.Title = "Workspaces"
	workspaceList.SetShowStatusBar(false)
	workspaceList.SetShowHelp(false)
	workspaceList.Styles.Title = design.Title
	workspaceList.Styles.FilterPrompt = design.Body
	workspaceList.Styles.StatusBarFilterCount = design.Caption
	workspaceList.Styles.NoItems = design.MutedStyle

	// Initialize search input with design tokens
	searchInput := textinput.New()
	searchInput.Placeholder = "Filter workspaces..."
	searchInput.Prompt = "/"
	searchInput.PromptStyle = design.MutedStyle
	searchInput.PlaceholderStyle = design.MutedStyle
	searchInput.TextStyle = design.Body

	return &AppModel{
		mux:           mux,
		workspaces:    []Workspace{},
		workspaceList: workspaceList,
		selectedWS:    "",
		tabs:          []PTYSession{},
		activeTab:     -1,
		currentView:   ViewDashboard,
		searchInput:   searchInput,
		width:         80,
		height:        24,
	}
}

// Init initializes the model and returns initial commands.
func (m *AppModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		// Initial workspace fetch
		func() tea.Msg {
			var result struct {
				Workspaces []workspace.Workspace `json:"workspaces"`
			}
			if err := m.mux.Call("workspace.list", map[string]any{}, &result); err != nil {
				return messages.DaemonDisconnected{Error: err}
			}

			items := make([]messages.WorkspaceItem, len(result.Workspaces))
			for i, ws := range result.Workspaces {
				items[i] = messages.WorkspaceItem{
					ID:    ws.ID,
					Name:  ws.WorkspaceName,
					Repo:  ws.Repo,
					State: string(ws.State),
				}
			}
			return messages.WorkspaceListReceived{Workspaces: items}
		},
	}
	// Start polling loop if poll command factory is set
	if m.pollCmd != nil {
		cmds = append(cmds, m.pollCmd())
	}
	return tea.Batch(cmds...)
}

// SetRenderer sets the view renderer. Called from tui.go to break the model↔views cycle.
func (m *AppModel) SetRenderer(r ViewRenderer) {
	m.renderer = r
}

// Update handles incoming messages by delegating to the update router.
func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Delegate to the update package router via a function variable.
	// The router is set during initialization in tui.go to avoid circular imports.
	if m.updateRouter != nil {
		return m.updateRouter(m, msg)
	}

	// Fallback: minimal inline handling when no router is set
	var cmds []tea.Cmd
	var listCmd tea.Cmd
	m.workspaceList, listCmd = m.workspaceList.Update(msg)
	cmds = append(cmds, listCmd)
	return m, tea.Batch(cmds...)
}

// UpdateFunc is the signature for the update router function.
type UpdateFunc func(m *AppModel, msg tea.Msg) (tea.Model, tea.Cmd)

// SetUpdateRouter sets the update router function. Called from tui.go.
func (m *AppModel) SetUpdateRouter(fn UpdateFunc) {
	m.updateRouter = fn
}

// SetPollCmd sets the polling command factory. Called from tui.go.
func (m *AppModel) SetPollCmd(fn func() tea.Cmd) {
	m.pollCmd = fn
}

// View renders the model using the configured view renderer.
func (m *AppModel) View() string {
	// Use the view renderer if set (preferred path)
	if m.renderer != nil {
		return m.renderer.Render(m)
	}

	// Fallback: inline rendering
	header := design.BorderStyle.Width(m.width).Render(
		design.Title.Render("nexus") +
			design.MutedStyle.Render(" ● connected"),
	)
	content := m.workspaceList.View()
	footer := design.MutedStyle.Render(
		"↑/k up • ↓/j down • / filter • ? more  |  q quit",
	)
	return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
}

// Accessors for update package

// CurrentView returns the current view.
func (m *AppModel) CurrentView() View {
	return m.currentView
}

// SetCurrentView sets the current view.
func (m *AppModel) SetCurrentView(view View) {
	m.currentView = view
}

// Modal returns the current modal state.
func (m *AppModel) Modal() ModalState {
	return m.modal
}

// SetModal sets the modal state.
func (m *AppModel) SetModal(state ModalState) {
	m.modal = state
}

// Toasts returns the current toasts.
func (m *AppModel) Toasts() []Toast {
	return m.toasts
}

// AddToast adds a toast notification. Max 3 toasts in queue (FIFO).
func (m *AppModel) AddToast(toast Toast) {
	m.toasts = append(m.toasts, toast)
	if len(m.toasts) > 3 {
		m.toasts = m.toasts[len(m.toasts)-3:]
	}
}

// RemoveToast removes a toast at the given index.
func (m *AppModel) RemoveToast(idx int) {
	if idx < 0 || idx >= len(m.toasts) {
		return
	}
	m.toasts = append(m.toasts[:idx], m.toasts[idx+1:]...)
}

// ClearToasts removes all toasts.
func (m *AppModel) ClearToasts() {
	m.toasts = []Toast{}
}

// WorkspaceList returns the workspace list.
func (m *AppModel) WorkspaceList() list.Model {
	return m.workspaceList
}

// SetWorkspaceList sets the workspace list.
func (m *AppModel) SetWorkspaceList(list list.Model) {
	m.workspaceList = list
}

// Workspaces returns the workspaces slice.
func (m *AppModel) Workspaces() []Workspace {
	return m.workspaces
}

// SetWorkspaces sets the workspaces slice.
func (m *AppModel) SetWorkspaces(ws []Workspace) {
	m.workspaces = ws
}

// SearchInput returns the search input model.
func (m *AppModel) SearchInput() textinput.Model {
	return m.searchInput
}

// SetSearchInput sets the search input model.
func (m *AppModel) SetSearchInput(input textinput.Model) {
	m.searchInput = input
}

// PTYPane returns the PTY pane.
func (m *AppModel) PTYPane() *pty.PtyPane {
	return m.ptyPane
}

// SetPTYPane sets the PTY pane.
func (m *AppModel) SetPTYPane(pane *pty.PtyPane) {
	m.ptyPane = pane
}

// Tabs returns the tabs slice.
func (m *AppModel) Tabs() []PTYSession {
	return m.tabs
}

// SetTabs sets the tabs slice.
func (m *AppModel) SetTabs(tabs []PTYSession) {
	m.tabs = tabs
}

// ActiveTab returns the active tab index.
func (m *AppModel) ActiveTab() int {
	return m.activeTab
}

// SetActiveTab sets the active tab index.
func (m *AppModel) SetActiveTab(idx int) {
	m.activeTab = idx
}

// SelectedWS returns the selected workspace ID.
func (m *AppModel) SelectedWS() string {
	return m.selectedWS
}

// SetSelectedWS sets the selected workspace ID.
func (m *AppModel) SetSelectedWS(id string) {
	m.selectedWS = id
}

// Width returns the terminal width.
func (m *AppModel) Width() int {
	return m.width
}

// SetWidth sets the terminal width.
func (m *AppModel) SetWidth(w int) {
	m.width = w
}

// Height returns the terminal height.
func (m *AppModel) Height() int {
	return m.height
}

// SetHeight sets the terminal height.
func (m *AppModel) SetHeight(h int) {
	m.height = h
}

// Filter returns the current filter text.
func (m *AppModel) Filter() string {
	return m.filter
}

// SetFilterText sets the filter text.
func (m *AppModel) SetFilterText(text string) {
	m.filter = text
}

// Forwards returns the port forwards slice.
func (m *AppModel) Forwards() []messages.PortForwardItem {
	return m.forwards
}

// SetForwards sets the port forwards slice.
func (m *AppModel) SetForwards(f []messages.PortForwardItem) {
	m.forwards = f
}

// SyncSessions returns the sync sessions slice.
func (m *AppModel) SyncSessions() []messages.SyncSessionItem {
	return m.syncSessions
}

// SetSyncSessions sets the sync sessions slice.
func (m *AppModel) SetSyncSessions(s []messages.SyncSessionItem) {
	m.syncSessions = s
}

// Mux returns the RPC multiplexed connection.
func (m *AppModel) Mux() *rpc.MuxConn {
	return m.mux
}

// CreateForm returns the current create form state.
func (m *AppModel) CreateForm() CreateForm {
	return m.createForm
}

// SetCreateForm sets the create form state.
func (m *AppModel) SetCreateForm(f CreateForm) {
	m.createForm = f
}

// UpdateStyles rebuilds styles for the current dimensions.
func UpdateStyles(width, height int) {
	// Styles are already design tokens; this is a no-op placeholder
	// for future use if dynamic styling is needed
}
