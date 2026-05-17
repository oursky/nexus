package views

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// OnrampView is the connection onboarding view.
// It guides users to connect to a daemon when no connection exists.
type OnrampView struct {
	width  int
	height int
	step   int // 0: host input, 1: port input, 2: connecting

	hostInput textinput.Model
	portInput textinput.Model
}

// NewOnrampView creates a new onramp view instance.
func NewOnrampView(width, height int) *OnrampView {
	hostInput := textinput.New()
	hostInput.Placeholder = "localhost"
	hostInput.Focus()

	portInput := textinput.New()
	portInput.Placeholder = "7777"
	portInput.Validate = func(s string) error { return nil } // TODO: validate port number

	return &OnrampView{
		width:     width,
		height:    height,
		step:      0,
		hostInput: hostInput,
		portInput: portInput,
	}
}

// Update handles user input in the onramp view.
func (v *OnrampView) Update(msg tea.Msg, m *model.AppModel) (*model.AppModel, tea.Cmd) {
	var result *model.AppModel = m
	var cmd tea.Cmd

	// Handle step transitions
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyEnter {
		if v.step == 0 {
			v.step = 1
			v.portInput.Focus()
			v.hostInput.Blur()
		} else if v.step == 1 {
			v.step = 2
			// TODO: Emit DaemonConnected message via commands.ConnectDaemon()
			// This will be implemented in Wave 7 (T16-T18)
		}
	}

	// Update active input
	switch v.step {
	case 0:
		var c tea.Cmd
		v.hostInput, c = v.hostInput.Update(msg)
		result = m
		cmd = c
	case 1:
		var c tea.Cmd
		v.portInput, c = v.portInput.Update(msg)
		result = m
		cmd = c
	case 2:
		// Connecting state — no input
	}

	return result, cmd
}

// View renders the onramp connection flow.
func (v *OnrampView) View(m *model.AppModel) string {
	// TODO: Implement full onramp rendering with:
	// - Step indicator
	// - Host input field (step 0)
	// - Port input field (step 1)
	// - Connecting spinner (step 2)
	// - Connection error display (if any)

	// Placeholder for now — Wave 7 will implement full rendering
	return lipgloss.NewStyle().
		Width(v.width).
		Height(v.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render("Onramp view — TODO in Wave 7")
}

// SetSize updates the view dimensions.
func (v *OnrampView) SetSize(width, height int) {
	v.width = width
	v.height = height
}
