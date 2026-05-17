package views

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// SpotlightView is the fuzzy search command palette.
// It filters actions and workspaces as the user types.
type SpotlightView struct {
	width  int
	height int

	input    textinput.Model
	cursor   int
	filtered []model.SpotlightItem
	open     bool
}

// NewSpotlightView creates a new spotlight view instance.
func NewSpotlightView(width, height int) *SpotlightView {
	input := textinput.New()
	input.Placeholder = "Search actions, workspaces..."
	input.Focus()

	t := design.GetTheme()
	s := design.GetStyles()

	input.Prompt = "> "
	input.PromptStyle = s.AccentStyle
	input.TextStyle = s.Body
	input.PlaceholderStyle = s.MutedStyle
	input.CompletionStyle = s.Caption.Foreground(t.Colors.TextSubtle)
	input.Cursor.Style = s.AccentStyle

	return &SpotlightView{
		width:    width,
		height:   height,
		input:    input,
		cursor:   0,
		filtered: []model.SpotlightItem{},
		open:     true,
	}
}

// Update handles user input in the spotlight view.
func (v *SpotlightView) Update(msg tea.Msg, m *model.AppModel) (*model.AppModel, tea.Cmd) {
	result := m
	var cmd tea.Cmd

	// Handle close
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyEsc {
		v.open = false
		return result, nil
	}

	// Update input
	var c tea.Cmd
	v.input, c = v.input.Update(msg)
	cmd = c

	// TODO: Update filtered list based on input
	// TODO: Handle cursor navigation
	// TODO: Handle enter to execute action
	// These will be implemented in Wave 7 (T16-T18)

	return result, cmd
}

// View renders the spotlight command palette.
func (v *SpotlightView) View(m *model.AppModel) string {
	// TODO: Implement full spotlight rendering with:
	// - Search input field
	// - Filtered result list
	// - Keyboard shortcuts
	// - Highlighted match text

	// Placeholder for now — Wave 7 will implement full rendering
	return lipgloss.NewStyle().
		Width(v.width).
		Height(v.height).
		Render("Spotlight view — TODO in Wave 7")
}

// SetSize updates the view dimensions.
func (v *SpotlightView) SetSize(width, height int) {
	v.width = width
	v.height = height
}
