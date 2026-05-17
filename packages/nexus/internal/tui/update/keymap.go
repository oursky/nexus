package update

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/commands"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
	"github.com/oursky/nexus/packages/nexus/internal/tui/pty"
)

// KeyHelpStyle is the style for key help text.
var KeyHelpStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("241")).
	Padding(0, 1)

// HandleKeyMsg handles keyboard input.
func HandleKeyMsg(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If PTY is active, forward keys to PTY
	if m.PTYPane() != nil && m.ActiveTab() >= 0 {
		if msg.Type == tea.KeyEsc {
			tabs := m.Tabs()
			activeIdx := m.ActiveTab()
			if activeIdx >= 0 && activeIdx < len(tabs) {
				if tabs[activeIdx].CancelFn != nil {
					tabs[activeIdx].CancelFn()
				}
				tabs = append(tabs[:activeIdx], tabs[activeIdx+1:]...)
				m.SetTabs(tabs)
				if len(tabs) > 0 {
					m.SetActiveTab(0)
				} else {
					m.SetActiveTab(-1)
					m.SetPTYPane(nil)
				}
			}
			return m, nil
		}
		return m, m.PTYPane().SendInputCmd(m.Mux(), msg)
	}

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
			m.SetCurrentView(model.ViewHelp)
			return m, nil
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
				m.SetCurrentView(model.ViewDetail)
				return m, nil
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
			m.SetCurrentView(model.ViewCreate)
			return m, nil
		case "t":
			if len(m.Workspaces()) > 0 {
				idx := wsList.Index()
				if idx >= 0 && idx < len(m.Workspaces()) {
					ws := m.Workspaces()[idx]
					m.SetSelectedWS(ws.ID)
					return m, pty.OpenPTYCmd(m.Mux(), ws.ID, m.Width(), m.Height()-4)
				}
			}
		case "?":
			m.SetCurrentView(model.ViewHelp)
			return m, nil
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
		m.SetCurrentView(model.ViewDashboard)
		return m, nil
	}
	return m, nil
}

// handleDetailKeys handles key bindings in detail view.
func handleDetailKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	selectedID := m.SelectedWS()

	switch msg.Type {
	case tea.KeyEsc:
		m.SetCurrentView(model.ViewDashboard)
		return m, nil
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
				return m, pty.OpenPTYCmd(m.Mux(), selectedID, m.Width(), m.Height()-4)
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
			m.SetCurrentView(model.ViewHelp)
			return m, nil
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
		m.SetCurrentView(model.ViewDashboard)
		return m, nil
	}
	return m, nil
}

// handleHelpKeys handles key bindings in help view.
func handleHelpKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.SetCurrentView(model.ViewDashboard)
		return m, nil
	case tea.KeyRunes:
		if msg.String() == "q" {
			m.SetCurrentView(model.ViewDashboard)
			return m, nil
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
