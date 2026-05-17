package update

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// KeyHelpStyle is the style for key help text.
var KeyHelpStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("241")).
	Padding(0, 1)

// HandleKeyMsg handles keyboard input.
func HandleKeyMsg(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// Handle modal/confirm dialogs first
	if m.Modal().Active {
		return handleConfirmModalKeys(m, msg)
	}

	// Handle toast dismissal with any key
	if len(m.Toasts()) > 0 {
		m.ClearToasts()
		return m, nil
	}

	// View-specific shortcuts
	switch m.CurrentView() {
	case model.ViewDashboard:
		var result tea.Model
		result, cmd = handleDashboardKeys(m, msg)
		m = result.(*model.AppModel)
	case model.ViewOnramp:
		var result tea.Model
		result, cmd = handleOnrampKeys(m, msg)
		m = result.(*model.AppModel)
	case model.ViewDetail:
		var result tea.Model
		result, cmd = handleDetailKeys(m, msg)
		m = result.(*model.AppModel)
	case model.ViewSpotlight:
		var result tea.Model
		result, cmd = handleSpotlightKeys(m, msg)
		m = result.(*model.AppModel)
	case model.ViewSync:
		var result tea.Model
		result, cmd = handleSyncKeys(m, msg)
		m = result.(*model.AppModel)
	case model.ViewCreate:
		var result tea.Model
		result, cmd = handleCreateKeys(m, msg)
		m = result.(*model.AppModel)
	case model.ViewHelp:
		var result tea.Model
		result, cmd = handleHelpKeys(m, msg)
		m = result.(*model.AppModel)
	default:
		// Global keys
		var result tea.Model
		result, cmd = handleGlobalKeys(m, msg)
		m = result.(*model.AppModel)
	}

	return m, cmd
}

// handleGlobalKeys handles global key bindings.
func handleGlobalKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

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

	return m, cmd
}

// handleDashboardKeys handles key bindings in dashboard view.
func handleDashboardKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg.Type {
	case tea.KeyEnter:
		wsList := m.WorkspaceList()
		if len(m.Workspaces()) > 0 {
			idx := wsList.Index()
			if idx >= 0 && idx < len(wsList.Items()) {
				_ = wsList.Items()[idx] // Use the workspace data
				return m, func() tea.Msg {
					return messages.ViewChanged{
						View: messages.ViewDetail,
						// WorkspaceID: ws.ID,
					}
				}
			}
		}
	case tea.KeyUp, tea.KeyCtrlK:
		wsList := m.WorkspaceList()
		wsList, cmd = wsList.Update(msg)
		m.SetWorkspaceList(wsList)
		return m, cmd
	case tea.KeyDown, tea.KeyCtrlJ:
		wsList := m.WorkspaceList()
		wsList, cmd = wsList.Update(msg)
		m.SetWorkspaceList(wsList)
		return m, cmd
	case tea.KeyRunes:
		switch msg.String() {
		case "k", "h":
			wsList := m.WorkspaceList()
			wsList, cmd = wsList.Update(tea.KeyMsg{Type: tea.KeyUp})
			m.SetWorkspaceList(wsList)
			return m, cmd
		case "j", "l":
			wsList := m.WorkspaceList()
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
		}
	}

	return m, cmd
}

// handleConfirmModalKeys handles key bindings in confirm modal.
func handleConfirmModalKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		// Confirm action
		m.SetModal(model.ModalState{Active: false})
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		// Cancel
		m.SetModal(model.ModalState{Active: false})
		return m, nil
	case tea.KeyRunes:
		switch msg.String() {
		case "y", "Y":
			m.SetModal(model.ModalState{Active: false})
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
	// TODO: Implement
	return m, nil
}

// handleDetailKeys handles key bindings in detail view.
func handleDetailKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// TODO: Implement
	return m, nil
}

// handleSpotlightKeys handles key bindings in spotlight view.
func handleSpotlightKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// TODO: Implement
	return m, nil
}

// handleSyncKeys handles key bindings in sync view.
func handleSyncKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// TODO: Implement
	return m, nil
}

// handleCreateKeys handles key bindings in create view.
func handleCreateKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// TODO: Implement
	return m, nil
}

// handleHelpKeys handles key bindings in help view.
func handleHelpKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// TODO: Implement
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
