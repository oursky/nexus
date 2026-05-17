package update

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
	"github.com/oursky/nexus/packages/nexus/internal/tui/pty"
)

// Router is the central message handler for the TUI.
// It dispatches incoming tea.Msg messages to appropriate handlers.
func Router(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	// Lifecycle messages
	case tea.KeyMsg:
		var result tea.Model
		result, cmd = HandleKeyMsg(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case tea.MouseMsg:
		var result tea.Model
		result, cmd = HandleMouseMsg(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case tea.WindowSizeMsg:
		var result tea.Model
		result, cmd = HandleWindowSize(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)

	// Connection messages
	case messages.DaemonConnected:
		var result tea.Model
		result, cmd = HandleDaemonConnected(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case messages.DaemonDisconnected:
		var result tea.Model
		result, cmd = HandleDaemonDisconnected(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case messages.ConnectionStatusMsg:
		var result tea.Model
		result, cmd = HandleConnectionStatus(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)

	// Workspace messages
	case messages.WorkspaceListReceived:
		var result tea.Model
		result, cmd = HandleWorkspaceListReceived(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case messages.WorkspaceSelected:
		var result tea.Model
		result, cmd = HandleWorkspaceSelected(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case messages.WorkspaceStateChanged:
		var result tea.Model
		result, cmd = HandleWorkspaceStateChanged(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case messages.WorkspaceDeleteConfirmed:
		var result tea.Model
		result, cmd = HandleWorkspaceDeleteConfirmed(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)

	// PTY messages
	case pty.PtyOpenedMsg:
		var result tea.Model
		result, cmd = handlePTYOpened(msg, m)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case pty.PtyDataMsg:
		var result tea.Model
		result, cmd = handlePTYData(msg, m)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case pty.PtyClosedMsg:
		var result tea.Model
		result, cmd = handlePTYClosed(msg, m)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case model.TabOpenedMsg:
		var result tea.Model
		result, cmd = handleTabOpened(msg, m)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case model.TabClosedMsg:
		var result tea.Model
		result, cmd = handleTabClosed(msg, m)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case model.TabSelectedMsg:
		var result tea.Model
		result, cmd = handleTabSelected(msg, m)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)

	// Lifecycle
	case messages.ViewChanged:
		var result tea.Model
		result, cmd = HandleViewChanged(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case messages.ToastShown:
		var result tea.Model
		result, cmd = HandleToastShown(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
	case toastDismissedMsg:
		m.RemoveToast(msg.index)
	}

	// Always pass messages through to the bubbles workspace list and search input
	// so built-in list navigation (arrows, filtering) still works
	var listCmd tea.Cmd
	wsList := m.WorkspaceList()
	wsList, listCmd = wsList.Update(msg)
	m.SetWorkspaceList(wsList)
	cmds = append(cmds, listCmd)

	searchInput := m.SearchInput()
	var inputCmd tea.Cmd
	searchInput, inputCmd = searchInput.Update(msg)
	m.SetSearchInput(searchInput)
	cmds = append(cmds, inputCmd)

	return m, tea.Batch(cmds...)
}

// Stub handlers for messages not yet implemented (T16-T18)
// These will be fully implemented in Wave 7

func HandleDaemonConnected(m *model.AppModel, msg messages.DaemonConnected) (tea.Model, tea.Cmd) {
	// TODO: Update connection state, show toast
	return m, nil
}

func HandleDaemonDisconnected(m *model.AppModel, msg messages.DaemonDisconnected) (tea.Model, tea.Cmd) {
	// TODO: Update connection state, show toast
	return m, nil
}

func HandleConnectionStatus(m *model.AppModel, msg messages.ConnectionStatusMsg) (tea.Model, tea.Cmd) {
	// TODO: Update header status indicator
	return m, nil
}

func HandleWorkspaceListReceived(m *model.AppModel, msg messages.WorkspaceListReceived) (tea.Model, tea.Cmd) {
	return handleWorkspaceListReceived(msg, m)
}

func HandleWorkspaceSelected(m *model.AppModel, msg messages.WorkspaceSelected) (tea.Model, tea.Cmd) {
	// TODO: Handle workspace selection
	return m, nil
}

func HandleWorkspaceStateChanged(m *model.AppModel, msg messages.WorkspaceStateChanged) (tea.Model, tea.Cmd) {
	// TODO: Update workspace state in list
	return m, nil
}

func HandleWorkspaceDeleteConfirmed(m *model.AppModel, msg messages.WorkspaceDeleteConfirmed) (tea.Model, tea.Cmd) {
	// TODO: Remove workspace from list, show toast
	return m, nil
}

func HandleTabOpened(m *model.AppModel, msg model.TabOpenedMsg) (tea.Model, tea.Cmd) {
	// TODO: Add new tab, switch to it
	return m, nil
}

func HandleTabClosed(m *model.AppModel, msg model.TabClosedMsg) (tea.Model, tea.Cmd) {
	// TODO: Remove tab, switch to another
	return m, nil
}

func HandleTabSwitched(m *model.AppModel, msg model.TabSelectedMsg) (tea.Model, tea.Cmd) {
	// TODO: Switch active tab
	return m, nil
}

func HandleViewChanged(m *model.AppModel, msg messages.ViewChanged) (tea.Model, tea.Cmd) {
	// TODO: Switch current view
	return m, nil
}

// toastDismissedMsg is sent when a toast auto-dismiss timer fires.
type toastDismissedMsg struct{ index int }

// toastTimeout returns the auto-dismiss duration for a toast kind.
func toastTimeout(kind messages.ToastKind) time.Duration {
	switch kind {
	case messages.ToastError:
		return 7 * time.Second
	case messages.ToastWarning:
		return 5 * time.Second
	default:
		return 3 * time.Second
	}
}

func HandleToastShown(m *model.AppModel, msg messages.ToastShown) (tea.Model, tea.Cmd) {
	t := model.Toast{Message: msg.Message, Kind: msg.Kind}
	m.AddToast(t)

	idx := len(m.Toasts()) - 1
	timeout := toastTimeout(msg.Kind)

	dismissCmd := tea.Tick(timeout, func(time.Time) tea.Msg {
		return toastDismissedMsg{index: idx}
	})

	return m, dismissCmd
}
