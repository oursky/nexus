package update

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/commands"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// KeyHelpStyle is the style for key help text.
var KeyHelpStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("241")).
	Padding(0, 1)

// HandleKeyMsg handles keyboard input.
func HandleKeyMsg(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle modal/confirm dialogs first
	if m.Modal().Active {
		return handleConfirmModalKeys(m, msg)
	}

	// Handle toast dismissal with any key
	if len(m.Toasts()) > 0 {
		m.ClearToasts()
		return m, nil
	}

	currentView := m.CurrentView()

	// View-specific shortcuts
	switch currentView {
	case model.ViewDashboard:
		result, c := handleDashboardKeys(m, msg)
		return result.(*model.AppModel), c
	case model.ViewOnramp:
		result, c := handleOnrampKeys(m, msg)
		return result.(*model.AppModel), c
	case model.ViewDetail:
		result, c := handleDetailKeys(m, msg)
		return result.(*model.AppModel), c
	case model.ViewSpotlight:
		result, c := handleSpotlightKeys(m, msg)
		return result.(*model.AppModel), c
	case model.ViewSync:
		result, c := handleSyncKeys(m, msg)
		return result.(*model.AppModel), c
	case model.ViewCreate:
		result, c := handleCreateKeys(m, msg)
		return result.(*model.AppModel), c
	case model.ViewHelp:
		result, c := handleHelpKeys(m, msg)
		return result.(*model.AppModel), c
	default:
		result, c := handleGlobalKeys(m, msg)
		return result.(*model.AppModel), c
	}
}

// handleGlobalKeys handles global key bindings.
func handleGlobalKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyRunes:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "?":
			return m, func() tea.Msg {
				return messages.ViewChanged{View: messages.ViewHelp}
			}
		}
	}
	return m, nil
}

// handleDashboardKeys handles key bindings in dashboard view.
func handleDashboardKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wsList := m.WorkspaceList()
	var cmd tea.Cmd

	switch msg.Type {
	case tea.KeyEnter:
		if len(m.Workspaces()) > 0 {
			idx := wsList.Index()
			if idx >= 0 && idx < len(m.Workspaces()) {
				ws := m.Workspaces()[idx]
				m.SetSelectedWS(ws.ID)
				return m, func() tea.Msg {
					return messages.ViewChanged{View: messages.ViewDetail}
				}
			}
		}
	case tea.KeyUp, tea.KeyCtrlK:
		wsList, cmd = wsList.Update(msg)
		m.SetWorkspaceList(wsList)
		return m, cmd
	case tea.KeyDown, tea.KeyCtrlJ:
		wsList, cmd = wsList.Update(msg)
		m.SetWorkspaceList(wsList)
		return m, cmd
	case tea.KeyRunes:
		switch msg.String() {
		case "k", "h":
			wsList, cmd = wsList.Update(tea.KeyMsg{Type: tea.KeyUp})
			m.SetWorkspaceList(wsList)
			return m, cmd
		case "j", "l":
			wsList, cmd = wsList.Update(tea.KeyMsg{Type: tea.KeyDown})
			m.SetWorkspaceList(wsList)
			return m, cmd
		case "/":
			searchInput := m.SearchInput()
			searchInput.Focus()
			m.SetSearchInput(searchInput)
			return m, nil
		case "c":
			return m, func() tea.Msg {
				return messages.ViewChanged{View: messages.ViewCreate}
			}
		case "t":
			if len(m.Workspaces()) > 0 {
				idx := wsList.Index()
				if idx >= 0 && idx < len(m.Workspaces()) {
					ws := m.Workspaces()[idx]
					m.SetSelectedWS(ws.ID)
					m.AddToast(model.Toast{
						Message: "Terminal: " + ws.Name,
						Kind:    messages.ToastInfo,
					})
					return m, nil
				}
			}
		}
	}

	return m, cmd
}

// handleConfirmModalKeys handles key bindings in confirm modal.
func handleConfirmModalKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter, tea.KeyEsc, tea.KeyCtrlC:
		m.SetModal(model.ModalState{Active: false})
		return m, nil
	case tea.KeyRunes:
		switch msg.String() {
		case "y", "Y":
			modal := m.Modal()
			m.SetModal(model.ModalState{Active: false})
			if modal.Action == "delete" && m.SelectedWS() != "" {
				return m, commands.DeleteWorkspace(m.Mux(), m.SelectedWS())
			}
			return m, nil
		case "n", "N":
			m.SetModal(model.ModalState{Active: false})
			return m, nil
		}
	}
	return m, nil
}

// handleOnrampKeys handles key bindings in onramp view.
func handleOnrampKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m, func() tea.Msg {
			return messages.ViewChanged{View: messages.ViewDashboard}
		}
	}
	return m, nil
}

// handleDetailKeys handles key bindings in detail view.
func handleDetailKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	selectedID := m.SelectedWS()

	switch msg.Type {
	case tea.KeyEsc:
		return m, func() tea.Msg {
			return messages.ViewChanged{View: messages.ViewDashboard}
		}
	case tea.KeyRunes:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "s":
			if selectedID != "" {
				return m, commands.StartWorkspace(m.Mux(), selectedID)
			}
		case "x":
			if selectedID != "" {
				return m, commands.StopWorkspace(m.Mux(), selectedID)
			}
		case "d":
			if selectedID != "" {
				m.SetModal(model.ModalState{
					Active: true,
					Title:  "Delete Workspace",
					Body:   "Are you sure you want to delete this workspace? This cannot be undone.",
					Action: "delete",
				})
				return m, nil
			}
		case "f":
			if selectedID != "" {
				return m, commands.ForkWorkspace(m.Mux(), selectedID)
			}
		case "t":
			if selectedID != "" {
				wsName := selectedID
				for _, ws := range m.Workspaces() {
					if ws.ID == selectedID {
						wsName = ws.Name
						break
					}
				}
				m.AddToast(model.Toast{
					Message: "Terminal: " + wsName,
					Kind:    messages.ToastInfo,
				})
				return m, nil
			}
		case "l":
			return m, func() tea.Msg {
				return messages.SpotlightRequested{}
			}
		case "y":
			return m, func() tea.Msg {
				return messages.SyncRequested{}
			}
		case "?":
			return m, func() tea.Msg {
				return messages.ViewChanged{View: messages.ViewHelp}
			}
		}
	}
	return m, nil
}

// handleSpotlightKeys handles key bindings in spotlight view.
func handleSpotlightKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, func() tea.Msg {
			return messages.SpotlightClosed{}
		}
	}
	return m, nil
}

// handleSyncKeys handles key bindings in sync view.
func handleSyncKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, func() tea.Msg {
			return messages.SyncClosed{}
		}
	}
	return m, nil
}

// handleCreateKeys handles key bindings in create view.
func handleCreateKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, func() tea.Msg {
			return messages.ViewChanged{View: messages.ViewDashboard}
		}
	}
	return m, nil
}

// handleHelpKeys handles key bindings in help view.
func handleHelpKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m, func() tea.Msg {
			return messages.ViewChanged{View: messages.ViewDashboard}
		}
	case tea.KeyRunes:
		if msg.String() == "q" {
			return m, func() tea.Msg {
				return messages.ViewChanged{View: messages.ViewDashboard}
			}
		}
	}
	return m, nil
}

// HandleWindowSize handles terminal resize.
func HandleWindowSize(m *model.AppModel, msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.SetWidth(msg.Width)
	m.SetHeight(msg.Height)

	// Resize the workspace list (reserve 4 lines for header + footer)
	wsList := m.WorkspaceList()
	wsList.SetSize(msg.Width, msg.Height-4)
	m.SetWorkspaceList(wsList)

	// Update PTY pane size (reserve 3 lines for footer + toasts)
	if m.PTYPane() != nil {
		m.PTYPane().Resize(msg.Width, msg.Height-3)
	}

	return m, nil
}
