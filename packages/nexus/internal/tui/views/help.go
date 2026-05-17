package views

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// HelpView displays keyboard shortcuts and help information.
// It's rendered as a modal overlay when requested.
type HelpView struct {
	width  int
	height int
	open   bool

	helpModel help.Model

	// Shortcut groups organized by view
	globalShortcuts    []model.Shortcut
	dashboardShortcuts []model.Shortcut
	detailShortcuts    []model.Shortcut
}

// NewHelpView creates a new help view instance.
func NewHelpView(width, height int) *HelpView {
	// Global shortcuts
	globalShortcuts := []model.Shortcut{
		{Key: "?", Description: "Toggle this help overlay"},
		{Key: "q", Description: "Quit"},
		{Key: "ctrl+c", Description: "Quit"},
		{Key: "/", Description: "Filter workspaces"},
		{Key: "p", Description: "Toggle spotlight palette"},
	}

	// Dashboard shortcuts
	dashboardShortcuts := []model.Shortcut{
		{Key: "↑/k", Description: "Move up"},
		{Key: "↓/j", Description: "Move down"},
		{Key: "Enter", Description: "Open workspace detail"},
		{Key: "n", Description: "Create new workspace"},
		{Key: "s", Description: "Start workspace"},
		{Key: "x", Description: "Stop workspace"},
	}

	// Detail shortcuts
	detailShortcuts := []model.Shortcut{
		{Key: "Tab", Description: "Switch between sections"},
		{Key: "t", Description: "Open terminal tab"},
		{Key: "+", Description: "Add port forward"},
		{Key: "-", Description: "Remove port forward"},
		{Key: "a", Description: "Start sync"},
		{Key: "z", Description: "Pause sync"},
	}

	s := design.GetStyles()

	h := help.New()
	h.Styles.ShortKey = s.AccentStyle
	h.Styles.ShortDesc = s.Body
	h.Styles.ShortSeparator = s.MutedStyle
	h.Styles.FullKey = s.AccentStyle
	h.Styles.FullDesc = s.Body
	h.Styles.FullSeparator = s.MutedStyle
	h.Styles.Ellipsis = s.MutedStyle

	return &HelpView{
		width:              width,
		height:             height,
		open:               false,
		helpModel:          h,
		globalShortcuts:    globalShortcuts,
		dashboardShortcuts: dashboardShortcuts,
		detailShortcuts:    detailShortcuts,
	}
}

// Update handles user input in the help view.
func (v *HelpView) Update(msg tea.Msg, m *model.AppModel) (*model.AppModel, tea.Cmd) {
	result, cmd := m, tea.Cmd(nil)

	// Close on any key except the toggle key
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if !(keyMsg.Type == tea.KeyRunes && string(keyMsg.Runes) == "?") {
			v.open = false
			return result, nil
		}
	}

	return result, cmd
}

// View renders the help overlay.
func (v *HelpView) View(m *model.AppModel) string {
	// TODO: Implement full help rendering with:
	// - Grouped shortcuts (global, dashboard, detail)
	// - Column layout for key + description
	// - Close instruction
	// - Styled with design tokens

	// Placeholder for now — Wave 7 will implement full rendering
	return lipgloss.NewStyle().
		Width(v.width).
		Height(v.height).
		Render("Help view — TODO in Wave 7")
}

// SetSize updates the view dimensions.
func (v *HelpView) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.helpModel.Width = width
}
