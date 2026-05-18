package update

import (
	"fmt"

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
		case "C", "O":
			// Open connect wizard to manage connection / switch daemon
			m.SetCurrentView(model.ViewOnramp)
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

// handleOnrampKeys handles key bindings in onramp/connect wizard view.
func handleOnrampKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wizard := m.Wizard()

	switch msg.Type {
	case tea.KeyTab:
		wizard.Step = (wizard.Step + 1) % 3
		focusWizardStep(&wizard)
		m.SetWizard(wizard)
		return m, nil

	case tea.KeyShiftTab:
		wizard.Step = (wizard.Step + 2) % 3
		focusWizardStep(&wizard)
		m.SetWizard(wizard)
		return m, nil

	case tea.KeyEnter:
		if wizard.Step < 2 {
			// Advance to next field
			wizard.Step++
			focusWizardStep(&wizard)
			m.SetWizard(wizard)
			return m, nil
		}
		// Submit on last field
		wizard.Busy = true
		wizard.Err = ""
		m.SetWizard(wizard)
		return m, func() tea.Msg {
			return messages.WizardSubmitMsg{
				Host: wizard.HostInput.Value(),
				Port: wizard.PortInput.Value(),
				Key:  wizard.KeyInput.Value(),
			}
		}

	case tea.KeyEsc:
		// Skip to localhost default
		wizard.HostInput.SetValue("localhost")
		wizard.PortInput.SetValue("7777")
		wizard.Busy = true
		wizard.Err = ""
		m.SetWizard(wizard)
		return m, func() tea.Msg {
			return messages.WizardSubmitMsg{
				Host: "localhost",
				Port: "7777",
				Key:  "",
			}
		}
	}

	// Pass printable keys to the active text input
	var cmd tea.Cmd
	switch wizard.Step {
	case 0:
		wizard.HostInput, cmd = wizard.HostInput.Update(msg)
	case 1:
		wizard.PortInput, cmd = wizard.PortInput.Update(msg)
	case 2:
		wizard.KeyInput, cmd = wizard.KeyInput.Update(msg)
	}
	m.SetWizard(wizard)
	return m, cmd
}

// focusWizardStep focuses the input at the given wizard step and blurs others.
func focusWizardStep(w *model.WizardState) {
	w.HostInput.Blur()
	w.PortInput.Blur()
	w.KeyInput.Blur()
	switch w.Step {
	case 0:
		w.HostInput.Focus()
	case 1:
		w.PortInput.Focus()
	case 2:
		w.KeyInput.Focus()
	}
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
		case "c":
			m.SetCurrentView(model.ViewConnect)
			return m, nil
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
	switch string(msg.Runes) {
	case "a":
		// Add port forward — for now, show toast with hint
		wsID := m.SelectedWS()
		var wsName string
		for _, ws := range m.Workspaces() {
			if ws.ID == wsID {
				wsName = ws.Name
				break
			}
		}
		m.AddToast(model.Toast{
			Kind:    messages.ToastInfo,
			Message: fmt.Sprintf("Add forward: use CLI — nexus port-forward %s --local <port> --remote <port>", wsName),
		})
		return m, nil
	case "r":
		// Remove selected forward
		forwards := m.Forwards()
		if len(forwards) > 0 {
			fwd := forwards[0] // TODO: track selected forward index
			m.AddToast(model.Toast{
				Kind:    messages.ToastInfo,
				Message: fmt.Sprintf("Removing forward %s...", fwd.Label),
			})
			return m, commands.RemoveForward(m.Mux(), m.SelectedWS(), fwd.ID)
		}
		return m, nil
	}
	// Default: Esc goes back
	m.SetCurrentView(model.ViewDetail)
	return m, nil
}

// handleSyncKeys handles key bindings in sync view.
func handleSyncKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wsID := m.SelectedWS()
	switch string(msg.Runes) {
	case "s":
		// Start sync — fetch sessions first, then if none, start one
		m.AddToast(model.Toast{
			Kind:    messages.ToastInfo,
			Message: "Starting sync...",
		})
		return m, tea.Batch(
			commands.StartSync(m.Mux(), wsID, ""),
			commands.FetchSyncSessions(m.Mux(), wsID),
		)
	case "p":
		sessions := m.SyncSessions()
		if len(sessions) > 0 {
			return m, commands.PauseSync(m.Mux(), sessions[0].ID)
		}
		return m, nil
	case "r":
		sessions := m.SyncSessions()
		if len(sessions) > 0 {
			return m, commands.ResumeSync(m.Mux(), sessions[0].ID)
		}
		return m, nil
	case "x":
		return m, commands.StopSync(m.Mux(), wsID)
	}
	// Default: Esc goes back
	m.SetCurrentView(model.ViewDetail)
	return m, nil
}

// handleCreateKeys handles key bindings in create view.
func handleCreateKeys(m *model.AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	form := m.CreateForm()

	switch msg.Type {
	case tea.KeyEsc:
		m.SetCurrentView(model.ViewDashboard)
		return m, nil

	case tea.KeyTab:
		form.Focus = (form.Focus + 1) % 3
		m.SetCreateForm(form)
		return m, nil

	case tea.KeyShiftTab:
		form.Focus = (form.Focus + 2) % 3
		m.SetCreateForm(form)
		return m, nil

	case tea.KeyEnter:
		// Validate
		if form.Name == "" {
			form.Err = "Name is required"
			m.SetCreateForm(form)
			return m, nil
		}
		if form.Repo == "" {
			form.Err = "Repo URL is required"
			m.SetCreateForm(form)
			return m, nil
		}
		// Create workspace via RPC
		form.Err = ""
		m.SetCreateForm(form)
		return m, commands.CreateWorkspace(m.Mux(), form.Name, form.Repo, form.Ref)

	case tea.KeyBackspace:
		switch form.Focus {
		case 0:
			if len(form.Name) > 0 {
				form.Name = form.Name[:len(form.Name)-1]
			}
		case 1:
			if len(form.Repo) > 0 {
				form.Repo = form.Repo[:len(form.Repo)-1]
			}
		case 2:
			if len(form.Ref) > 0 {
				form.Ref = form.Ref[:len(form.Ref)-1]
			}
		}
		m.SetCreateForm(form)
		return m, nil

	case tea.KeyRunes:
		ch := msg.String()
		switch form.Focus {
		case 0:
			form.Name += ch
		case 1:
			form.Repo += ch
		case 2:
			form.Ref += ch
		}
		form.Err = ""
		m.SetCreateForm(form)
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
