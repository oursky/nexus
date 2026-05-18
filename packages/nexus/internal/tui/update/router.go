package update

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/tui/commands"
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

	// Connection wizard messages
	case messages.ConnReadyMsg:
		// Connection established from wizard — store mux, start fetching workspaces
		m.SetMux(msg.Mux)
		m.SetConnected(true)
		m.SetCurrentView(model.ViewDashboard)
		// Re-initialize poll command with new mux
		var cmds []tea.Cmd
		cmds = append(cmds, func() tea.Msg {
			var result struct {
				Workspaces []workspace.Workspace `json:"workspaces"`
			}
			if err := msg.Mux.Call("workspace.list", map[string]any{}, &result); err != nil {
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
		})
		return m, tea.Batch(cmds...)

	case messages.ConnFailedMsg:
		// Connection failed from wizard — show error in wizard
		wizard := m.Wizard()
		wizard.Busy = false
		wizard.Err = msg.Error.Error()
		m.SetWizard(wizard)
		m.SetCurrentView(model.ViewOnramp)
		return m, nil

	case messages.WizardSubmitMsg:
		// Wizard submitted — try to connect
		host := msg.Host
		if host == "" {
			host = "localhost"
		}
		port := 7777
		if p, err := strconv.Atoi(msg.Port); err == nil {
			port = p
		}
		key := msg.Key
		if key == "" {
			key = "" // empty = default SSH key
		}

		return m, func() tea.Msg {
			// Save profile
			p := &profile.Profile{
				Name:            host,
				Host:            host,
				Port:            port,
				SSHIdentityFile: key,
			}
			if rpc.IsLoopbackHost(host) {
				// Local: try to get token from daemon
				p.Token = "" // will use local token
			} else {
				// Remote: fetch token via SSH
				p.Token = "" // TODO: implement SSH token fetch
			}
			if err := profile.SaveDefault(p); err != nil {
				return messages.ConnFailedMsg{Error: fmt.Errorf("save profile: %w", err)}
			}
			// Try connecting
			ws, err := rpc.EnsureDaemon()
			if err != nil {
				return messages.ConnFailedMsg{Error: fmt.Errorf("connect: %w", err)}
			}
			mux := rpc.NewMuxConn(ws)
			return messages.ConnReadyMsg{Mux: mux}
		}

	// Workspace messages
	case messages.WorkspaceListReceived:
		var result tea.Model
		result, cmd = HandleWorkspaceListReceived(m, msg)
		m = result.(*model.AppModel)
		cmds = append(cmds, cmd)
		// Schedule next poll
		cmds = append(cmds, commands.PollWorkspacesCmd(m.Mux()))
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

	// Spotlight messages
	case messages.SpotlightRefreshed:
		m.SetForwards(msg.Items)
		return m, nil

	case messages.PortForwardAdded:
		m.SetForwards(append(m.Forwards(), messages.PortForwardItem{
			ID:         msg.ForwardID,
			LocalPort:  msg.LocalPort,
			RemotePort: msg.RemotePort,
			Label:      msg.Label,
			Status:     "active",
		}))
		return m, nil

	case messages.PortForwardRemoved:
		var remaining []messages.PortForwardItem
		for _, f := range m.Forwards() {
			if f.ID != msg.ForwardID {
				remaining = append(remaining, f)
			}
		}
		m.SetForwards(remaining)
		return m, nil

	// Sync messages
	case messages.SyncListReceived:
		m.SetSyncSessions(msg.Sessions)
		return m, nil

	case messages.SyncStatusReceived:
		m.AddToast(model.Toast{
			Message: "Sync: " + msg.Status,
			Kind:    messages.ToastSuccess,
		})
		return m, nil

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
		// Re-listen on the same channel
		for _, t := range m.Tabs() {
			if t.ID == msg.SessionID {
				cmds = append(cmds, pty.ListenPTYCmd(t.DataCh, msg.SessionID))
				break
			}
		}
		// Re-listen on the same channel
		for _, t := range m.Tabs() {
			if t.ID == msg.SessionID && t.DataCh != nil {
				cmds = append(cmds, pty.ListenPTYCmd(t.DataCh, msg.SessionID))
				break
			}
		}
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

	// Pass non-key messages through to bubbles components.
	// Key messages are handled by HandleKeyMsg above which already updates
	// the list for navigation keys; we skip the passthrough to avoid
	// double-processing (e.g., list intercepting Enter for its own selection).
	if _, ok := msg.(tea.KeyMsg); !ok {
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
	}

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
	// Update workspace in the workspaces slice
	workspaces := m.Workspaces()
	for i, ws := range workspaces {
		if ws.ID == msg.ID {
			workspaces[i].State = msg.State
			break
		}
	}
	m.SetWorkspaces(workspaces)

	// Rebuild list items
	items := make([]list.Item, len(workspaces))
	for i, ws := range workspaces {
		items[i] = messages.WorkspaceItem{
			ID:    ws.ID,
			Name:  ws.Name,
			Repo:  ws.Repo,
			State: ws.State,
		}
	}
	wsList := m.WorkspaceList()
	wsList.SetItems(items)
	m.SetWorkspaceList(wsList)

	// Show toast
	return m, func() tea.Msg {
		return messages.ToastShown{
			Message: "Workspace " + msg.ID + ": " + msg.State,
			Kind:    messages.ToastInfo,
		}
	}
}

func HandleWorkspaceDeleteConfirmed(m *model.AppModel, msg messages.WorkspaceDeleteConfirmed) (tea.Model, tea.Cmd) {
	// Remove from workspaces slice
	workspaces := m.Workspaces()
	filtered := make([]model.Workspace, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws.ID != msg.ID {
			filtered = append(filtered, ws)
		}
	}
	m.SetWorkspaces(filtered)

	// Rebuild list items
	items := make([]list.Item, len(filtered))
	for i, ws := range filtered {
		items[i] = messages.WorkspaceItem{
			ID:    ws.ID,
			Name:  ws.Name,
			Repo:  ws.Repo,
			State: ws.State,
		}
	}
	wsList := m.WorkspaceList()
	wsList.SetItems(items)
	m.SetWorkspaceList(wsList)

	// Navigate back to dashboard
	m.SetCurrentView(model.ViewDashboard)

	// Show toast
	return m, func() tea.Msg {
		return messages.ToastShown{
			Message: "Workspace deleted",
			Kind:    messages.ToastSuccess,
		}
	}
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
	newView := model.View(msg.View)
	m.SetCurrentView(newView)
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

// isLocalhost checks if the host is a local address.
func isLocalhost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}
