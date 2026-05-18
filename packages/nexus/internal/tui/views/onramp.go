package views

import (
	"strconv"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// OnrampView is the connection onboarding view.
// It guides users to connect to a daemon when no connection exists.
type OnrampView struct {
	width  int
	height int
}

// NewOnrampView creates a new onramp view instance.
func NewOnrampView(width, height int) *OnrampView {
	return &OnrampView{width: width, height: height}
}

// View renders the onramp connection flow.
func (v *OnrampView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	wizard := m.Wizard()

	w := m.Width()
	h := m.Height()
	if w < 40 {
		w = 40
	}
	if h < 12 {
		h = 12
	}

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(colors.Text).
		MarginBottom(1).
		Render("Connect to Daemon")

	// Field labels and inputs
	labels := []string{"Host", "Port", "SSH Identity"}
	inputs := []*textinput.Model{&wizard.HostInput, &wizard.PortInput, &wizard.KeyInput}
	placeholders := []string{"localhost", strconv.Itoa(design.DefaultDaemonPort), "~/.ssh/id_ed25519"}

	fieldWidth := min(50, w-24)

	var fields []string
	for i, label := range labels {
		labelStyle := lipgloss.NewStyle().
			Foreground(colors.TextMuted).
			Width(14).
			Render(label + ":")

		input := inputs[i]
		input.Width = fieldWidth
		if input.Value() == "" {
			input.Placeholder = placeholders[i]
		}

		if i == wizard.Step {
			input.TextStyle = lipgloss.NewStyle().Foreground(colors.Accent)
			input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colors.Accent).Bold(true)
		} else {
			input.TextStyle = lipgloss.NewStyle().Foreground(colors.TextMuted)
			input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colors.Border)
		}

		row := lipgloss.JoinHorizontal(lipgloss.Top, labelStyle, " ", input.View())
		fields = append(fields, row)
	}

	form := lipgloss.JoinVertical(lipgloss.Left, fields...)

	// Error message
	var errBlock string
	if wizard.Err != "" {
		errBlock = lipgloss.NewStyle().
			Foreground(colors.Error).
			MarginTop(1).
			Render("Error: " + wizard.Err)
	}

	// Status
	var status string
	if wizard.Busy {
		status = lipgloss.NewStyle().
			Foreground(colors.Accent).
			MarginTop(1).
			Render("Connecting...")
	}

	// Hints
	hints := lipgloss.NewStyle().
		Foreground(colors.TextMuted).
		MarginTop(1).
		Render("Tab next • Shift+Tab prev • Enter submit • Esc localhost default")

	// Combine inner content
	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		form,
		errBlock,
		status,
		hints,
	)

	// Bordered box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), true).
		BorderForeground(colors.Border).
		Padding(2, 4).
		Width(min(60, w-4)).
		Align(lipgloss.Center, lipgloss.Center)

	// Center in the body area (height minus header/footer)
	bodyHeight := h - 4
	if bodyHeight < 10 {
		bodyHeight = 10
	}

	centered := lipgloss.NewStyle().
		Width(w).
		Height(bodyHeight).
		Align(lipgloss.Center, lipgloss.Center).
		Render(boxStyle.Render(content))

	return centered
}

// SetSize updates the view dimensions.
func (v *OnrampView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// focusStep focuses the input at the given wizard step and blurs others.
func (v *OnrampView) focusStep(w *model.WizardState) {
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
