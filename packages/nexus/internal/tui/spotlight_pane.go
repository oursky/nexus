package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
)

// renderSpotlightSidebar renders the right-side spotlight sidebar for the
// three-pane split layout.
func renderSpotlightSidebar(m *Model, width, height int) string {
	focused := m.sidebarFocused

	headerStyle := sectionLabelStyle.Bold(false)
	if focused {
		headerStyle = accentStyle
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render("SPOTLIGHT"))
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", max(width-1, 1))))
	b.WriteString("\n")

	if len(m.sidebarFwds) == 0 {
		b.WriteString(mutedStyle.Render("No forwards"))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("  [n] forward port"))
		b.WriteString("\n")
	} else {
		for i, f := range m.sidebarFwds {
			if f == nil {
				continue
			}
			var indicator string
			if f.State == spotlight.ForwardStateActive {
				indicator = statusOkStyle.Render("●")
			} else {
				indicator = mutedStyle.Render("○")
			}
			// Show from the user's perspective: what local port they connect to.
			// Format: "<localPort>" when local==remote, "<localPort>→<remotePort>"
			// when they differ (e.g. custom local port assignment).
			var portStr string
			if f.LocalPort == f.RemotePort {
				portStr = fmt.Sprintf("%d", f.LocalPort)
			} else {
				portStr = fmt.Sprintf("%d→%d", f.LocalPort, f.RemotePort)
			}
			row := truncate(portStr, max(width-3, 8))
			var rendered string
			if i == m.sidebarSel {
				rendered = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#FFFDF5")).
					Render("›" + indicator + " " + row)
			} else {
				rendered = "  " + indicator + " " + row
			}
			b.WriteString(rendered)
			b.WriteString("\n")
		}
	}

	if focused {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("[n] add  [x] del"))
		b.WriteString("\n")
	}

	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}
