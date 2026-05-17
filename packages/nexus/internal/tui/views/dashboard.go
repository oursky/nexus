package views

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// DashboardView renders the workspace list with project groups.
// This is the main landing view showing all workspaces organized by project.
type DashboardView struct {
	width      int
	height     int
	showFilter bool
}

// NewDashboardView creates a new dashboard view instance.
func NewDashboardView(width, height int) *DashboardView {
	return &DashboardView{
		width:      width,
		height:     height,
		showFilter: false,
	}
}

// Update handles user input in the dashboard view.
func (v *DashboardView) Update(msg tea.Msg, m *model.AppModel) (*model.AppModel, tea.Cmd) {
	result, cmd := m, tea.Cmd(nil)

	// Handle filter toggle
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyRunes && string(keyMsg.Runes) == "/" {
		v.showFilter = !v.ShowFilter()
		return result, nil
	}

	// Update model's list
	wsList := m.WorkspaceList()
	var listCmd tea.Cmd
	wsList, listCmd = wsList.Update(msg)
	m.SetWorkspaceList(wsList)
	cmd = tea.Batch(cmd, listCmd)

	// For now, pass through to existing tui behavior
	// TODO: Implement workspace selection, filtering, actions
	// These will be filled in by Wave 7 (T16-T18)

	return result, cmd
}

// View renders the dashboard workspace list.
func (v *DashboardView) View(m *model.AppModel) string {
	wsList := m.WorkspaceList()
	wsList.SetSize(m.Width(), m.Height()-4)
	return wsList.View()
}

// SetSize updates the view dimensions.
func (v *DashboardView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// ShowFilter returns whether the filter input is visible.
func (v *DashboardView) ShowFilter() bool {
	return v.showFilter
}
