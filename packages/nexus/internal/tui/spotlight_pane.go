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
			local := "localhost"
			if f.TargetHost != "" {
				local = f.TargetHost
			}
			row := fmt.Sprintf(":%d → %s", f.RemotePort, local)
			row = truncate(row, max(width-3, 8))
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
