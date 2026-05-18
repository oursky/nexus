package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

var noProfileSpinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NoProfileView renders the connection profile manager panel.
// It shows when no daemon connection exists, presenting options
// to start a local daemon or connect to a remote host.
type NoProfileView struct{}

func NewNoProfileView() *NoProfileView {
	return &NoProfileView{}
}

func (v *NoProfileView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	w := m.Width()
	h := m.Height()

	np := m.NoProfile()

	modalWidth := 54

	// ─── Styles ───────────────────────────────────────
	titleStyle := lipgloss.NewStyle().Foreground(colors.Text).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)
	accentStyle := lipgloss.NewStyle().Foreground(colors.Accent)
	errStyle := lipgloss.NewStyle().Foreground(colors.Error)
	warningStyle := lipgloss.NewStyle().Foreground(colors.Warning)
	dimStyle := lipgloss.NewStyle().Foreground(colors.Border)

	spin := noProfileSpinFrames[np.SpinIdx%len(noProfileSpinFrames)]

	// ─── Content ──────────────────────────────────────
	var b strings.Builder

	switch {
	case np.Checking:
		fmt.Fprintf(&b, "%s\n", dimStyle.Render(spin+" Checking for local daemon…"))
	case np.Busy:
		fmt.Fprintf(&b, "%s\n", dimStyle.Render(spin+" Starting daemon…"))
	default:
		if np.Err != "" {
			fmt.Fprintf(&b, "%s\n\n", errStyle.Render("✗ "+np.Err))
		} else {
			fmt.Fprintf(&b, "%s\n\n", warningStyle.Render("No daemon connection found."))
		}

		entries := []string{
			"Start local daemon",
			"Connect to remote host",
		}
		for i, label := range entries {
			prefix := "   "
			style := mutedStyle
			if i == np.Sel {
				prefix = "▶  "
				style = accentStyle
			}
			fmt.Fprintf(&b, "%s\n", style.Render(prefix+label))
		}

		// Footer hint
		if np.Sel == 0 {
			fmt.Fprintf(&b, "%s", mutedStyle.Render("starts a local nexus daemon"))
		} else {
			fmt.Fprintf(&b, "%s", mutedStyle.Render("enter host address and SSH key"))
		}
	}

	inner := lipgloss.JoinVertical(lipgloss.Center,
		titleStyle.Render("  nexus  "),
		"",
		b.String(),
		"",
		mutedStyle.Render("[ enter ] select    [ esc / q ] quit"),
	)

	// ─── Dimmed overlay + centered modal ─────────────
	return renderModal(colors, inner, w, h, modalWidth)
}

// renderModal renders a centered bordered box on top of a
// dimmed full-screen background. Used by both no-profile
// and onramp views for visual consistency.
func renderModal(colors design.ColorTokens, content string, termW, termH, boxW int) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colors.Border).
		Padding(2, 3).
		Width(boxW).
		Render(content)

	overlay := lipgloss.NewStyle().
		Width(termW).
		Height(termH).
		Background(colors.BgOverlay)

	centered := lipgloss.Place(termW, termH,
		lipgloss.Center, lipgloss.Center,
		box,
	)

	// Pad to terminal width so background fills the full width
	lines := strings.Split(centered, "\n")
	var padded []string
	for _, line := range lines {
		lineW := lipgloss.Width(line)
		if lineW < termW {
			line += strings.Repeat(" ", termW-lineW)
		}
		padded = append(padded, line)
	}
	return overlay.Render(strings.Join(padded, "\n"))
}

