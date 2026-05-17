package views

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// DetailView displays detailed information about a selected workspace.
// Shows tabs, port forwards, sync status, and action buttons.
type DetailView struct {
	width  int
	height int

	// These will be populated by Wave 7 handlers
	tabs       []model.Tab
	portFwd    []model.PortForward
	syncStatus string
}

// NewDetailView creates a new detail view instance.
func NewDetailView(width, height int) *DetailView {
	return &DetailView{
		width:      width,
		height:     height,
		tabs:       []model.Tab{},
		portFwd:    []model.PortForward{},
		syncStatus: "inactive",
	}
}

// Update handles user input in the detail view.
func (v *DetailView) Update(msg tea.Msg, m *model.AppModel) (*model.AppModel, tea.Cmd) {
	result, cmd := m, tea.Cmd(nil)

	// TODO: Handle tab switching, port forward actions, sync controls
	// These will be implemented in Wave 7 (T16-T18)

	_ = msg

	return result, cmd
}

// View renders the workspace detail view.
func (v *DetailView) View(m *model.AppModel) string {
	// TODO: Implement full detail rendering with:
	// - Workspace metadata
	// - Tab bar with open terminal tabs
	// - Port forward list (add/remove)
	// - Sync status and controls (start/pause/resume/stop)
	// - Action buttons

	// Placeholder for now — Wave 7 will implement full rendering
	return lipgloss.NewStyle().
		Width(v.width).
		Height(v.height).
		Render("Detail view — TODO in Wave 7")
}

// SetSize updates the view dimensions.
func (v *DetailView) SetSize(width, height int) {
	v.width = width
	v.height = height
}
