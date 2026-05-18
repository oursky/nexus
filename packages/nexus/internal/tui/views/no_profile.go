package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

var noProfileSpinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NoProfileView renders the "start local daemon or connect remote" choice panel.
type NoProfileView struct{}

func NewNoProfileView() *NoProfileView {
	return &NoProfileView{}
}

func (v *NoProfileView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	w := m.Width()
	h := m.Height()

	noProfile := m.NoProfile()

	sep := lipgloss.NewStyle().
		Foreground(colors.Border).
		Render(strings.Repeat("─", w-4))

	titleStyle := lipgloss.NewStyle().
		Foreground(colors.Text).
		Bold(true)

	mutedStyle := lipgloss.NewStyle().
		Foreground(colors.TextMuted)

	accentStyle := lipgloss.NewStyle().
		Foreground(colors.Accent)

	errStyle := lipgloss.NewStyle().
		Foreground(colors.Error)

	warningStyle := lipgloss.NewStyle().
		Foreground(colors.Warning)

	keyStyle := lipgloss.NewStyle().
		Foreground(colors.TextMuted)

	spin := noProfileSpinFrames[noProfile.SpinIdx%len(noProfileSpinFrames)]

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", titleStyle.Render("nexus"))
	fmt.Fprintf(&b, "%s\n\n", sep)

	switch {
	case noProfile.Checking:
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(spin+" Checking for local daemon…"))

	case noProfile.Busy:
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(spin+" Starting daemon…"))

	default:
		if noProfile.Err != "" {
			fmt.Fprintf(&b, "%s\n\n", errStyle.Render("✗ "+noProfile.Err))
		} else {
			fmt.Fprintf(&b, "%s\n\n", warningStyle.Render("No daemon running."))
		}

		entries := []struct{ label, hint string }{
			{"Start local daemon", "nexus daemon start"},
			{"Connect to remote host…", "enter host/port/SSH key"},
		}
		for i, e := range entries {
			if i == noProfile.Sel {
				fmt.Fprintf(&b, "%s   %s\n",
					accentStyle.Render("▶  "+e.label),
					mutedStyle.Render("("+e.hint+")"),
				)
			} else {
				fmt.Fprintf(&b, "%s   %s\n",
					"   "+keyStyle.Render(e.label),
					mutedStyle.Render("("+e.hint+")"),
				)
			}
		}

		fmt.Fprintf(&b, "\n%s   %s",
			accentStyle.Render("[ enter  select ]"),
			mutedStyle.Render("[ esc / q  quit ]"),
		)
	}

	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colors.Border).
		Padding(1, 2).
		Width(w).
		Render(b.String())

	// Center the modal box within a full-screen dimmed overlay
	overlayStyle := lipgloss.NewStyle().
		Width(w).
		Height(h).
		Background(colors.BgOverlay)

	centered := lipgloss.Place(w, h,
		lipgloss.Center, lipgloss.Center,
		modalBox,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(colors.BgOverlay),
	)

	return overlayStyle.Render(centered)
}
