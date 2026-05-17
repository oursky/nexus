package views

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// SyncView displays sync status and controls for a workspace.
// Shows file sync progress, conflicts, and action buttons.
type SyncView struct {
	width  int
	height int

	status   string // "inactive", "running", "paused", "conflict"
	files    []model.SyncFile
	progress float64
	bar      progress.Model
}

// NewSyncView creates a new sync view instance.
func NewSyncView(width, height int) *SyncView {
	t := design.GetTheme()
	s := design.GetStyles()

	bar := progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(width-2*design.SpaceLG),
	)
	bar.FullColor = string(t.Colors.Accent)
	bar.EmptyColor = string(t.Colors.BgSubtle)
	bar.PercentageStyle = s.Caption

	return &SyncView{
		width:    width,
		height:   height,
		status:   "inactive",
		files:    []model.SyncFile{},
		progress: 0.0,
		bar:      bar,
	}
}

// Update handles user input in the sync view.
func (v *SyncView) Update(msg tea.Msg, m *model.AppModel) (*model.AppModel, tea.Cmd) {
	result, cmd := m, tea.Cmd(nil)

	// Update progress bar
	barModel, barCmd := v.bar.Update(msg)
	v.bar = barModel.(progress.Model)
	cmd = tea.Batch(cmd, barCmd)

	// TODO: Handle sync actions (start, pause, resume, stop)
	// TODO: Handle file conflict resolution
	// These will be implemented in Wave 7 (T16-T18)

	return result, cmd
}

// View renders the sync management view.
func (v *SyncView) View(m *model.AppModel) string {
	// TODO: Implement full sync rendering with:
	// - Status indicator
	// - File list with sync states
	// - Progress bar
	// - Action buttons (start/pause/resume/stop)
	// - Conflict resolution UI

	// Placeholder for now — Wave 7 will implement full rendering
	return lipgloss.NewStyle().
		Width(v.width).
		Height(v.height).
		Render("Sync view — TODO in Wave 7")
}

// SetSize updates the view dimensions.
func (v *SyncView) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.bar.Width = width - 2*design.SpaceLG
}

// SetProgress updates the sync progress (0.0 to 1.0).
func (v *SyncView) SetProgress(p float64) {
	v.progress = p
	v.bar.SetPercent(p)
}
