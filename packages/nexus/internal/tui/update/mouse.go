package update

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// HandleMouseMsg handles mouse input.
func HandleMouseMsg(m *model.AppModel, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// Handle modal clicks
	if m.Modal().Active {
		return handleConfirmModalMouse(m, msg)
	}

	// Handle toast clicks (dismiss)
	if len(m.Toasts()) > 0 {
		m.ClearToasts()
		return m, nil
	}

	// View-specific mouse handling
	switch m.CurrentView() {
	case model.ViewDashboard:
		var result tea.Model
		result, cmd = handleDashboardMouse(m, msg)
		m = result.(*model.AppModel)
	case model.ViewDetail:
		var result tea.Model
		result, cmd = handleDetailMouse(m, msg)
		m = result.(*model.AppModel)
	}

	return m, cmd
}

// handleConfirmModalMouse handles mouse clicks in confirm modal.
func handleConfirmModalMouse(m *model.AppModel, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.MouseLeft {
		// For now, left-click anywhere dismisses the modal
		m.SetModal(model.ModalState{Active: false})
		return m, nil
	}

	return m, nil
}

// handleDashboardMouse handles mouse clicks in dashboard view.
func handleDashboardMouse(m *model.AppModel, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// TODO: Implement workspace item click detection
	return m, nil
}

// handleDetailMouse handles mouse clicks in detail view.
func handleDetailMouse(m *model.AppModel, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// TODO: Implement button clicks, tab clicks
	return m, nil
}
