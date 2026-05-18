package views

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// OnrampView is the connection wizard view.
// It renders a centered form overlay for daemon connection settings.
type OnrampView struct {
	width  int
	height int
}

func NewOnrampView(width, height int) *OnrampView {
	return &OnrampView{width: width, height: height}
}

func (v *OnrampView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	wizard := m.Wizard()
	w := m.Width()
	h := m.Height()

	modalWidth := 54

	// ─── Styles ───────────────────────────────────────
	titleStyle := lipgloss.NewStyle().Foreground(colors.Text).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(colors.TextMuted).Width(4)
	errStyle := lipgloss.NewStyle().Foreground(colors.Error)
	mutedStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)
	accentStyle := lipgloss.NewStyle().Foreground(colors.Accent)

	// ─── Form fields ──────────────────────────────────
	labels := []string{"Host", "Port", "Key"}
	inputs := []*textinput.Model{&wizard.HostInput, &wizard.PortInput, &wizard.KeyInput}
	placeholders := []string{"localhost", strconv.Itoa(design.DefaultDaemonPort), "~/.ssh/id_ed25519"}

	fieldWidth := modalWidth - 8 // account for label + border padding
	if fieldWidth < 20 {
		fieldWidth = 20
	}

	var fields []string
	for i, lbl := range labels {
		in := inputs[i]
		in.Width = fieldWidth
		if in.Value() == "" {
			in.Placeholder = placeholders[i]
		}
		if i == wizard.Step {
			in.TextStyle = accentStyle
		} else {
			in.TextStyle = mutedStyle
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top,
			labelStyle.Render(lbl),
			" ",
			in.View(),
		)
		fields = append(fields, row)
	}
	form := strings.Join(fields, "\n")

	// ─── Status / Error ───────────────────────────────
	var status string
	if wizard.Err != "" {
		status = errStyle.Render("✗ " + wizard.Err)
	} else if wizard.Busy {
		status = accentStyle.Render("Connecting…")
	}

	// ─── Assemble inner content ───────────────────────
	inner := lipgloss.JoinVertical(lipgloss.Center,
		titleStyle.Render("Connect to Daemon"),
		"",
		form,
		"",
		status,
		"",
		mutedStyle.Render("Tab next • Enter connect • Esc cancel"),
	)

	return renderModal(colors, inner, w, h, modalWidth)
}

func (v *OnrampView) SetSize(width, height int) {
	v.width = width
	v.height = height
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

