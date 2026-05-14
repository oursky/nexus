package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
)

// renderSpotlightSidebar renders the right-side spotlight sidebar for the
// three-pane split layout. width/height are the inner content dimensions
// (already accounting for any surrounding border).
func renderSpotlightSidebar(m *Model, width, height int) string {
	focused := m.sidebarFocused

	headerStyle := sectionLabelStyle.Bold(false)
	if focused {
		headerStyle = accentStyle
	}

	// Count active forwards.
	activeCount := 0
	for _, f := range m.sidebarFwds {
		if f != nil && f.State == spotlight.ForwardStateActive {
			activeCount++
		}
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render("SPOTLIGHT"))
	b.WriteString("\n")

	// Toggle-all hint with active count badge.
	toggleLine := mutedStyle.Render("[ a ] Toggle all")
	if activeCount > 0 {
		toggleLine += "  " + statusOkStyle.Render(fmt.Sprintf("● %d active", activeCount))
	}
	b.WriteString(toggleLine)
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
			// when they differ (daemon-assigned ephemeral proxy for VM workspaces).
			var portStr string
			if f.LocalPort == f.RemotePort {
				portStr = fmt.Sprintf("%d", f.LocalPort)
			} else {
				portStr = fmt.Sprintf("%d→%d", f.LocalPort, f.RemotePort)
			}
			row := truncate(portStr, max(width-3, 8))
			var rendered string
			if i == m.sidebarSel && focused {
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

	// Discovered ports section: ports detected inside the workspace that are
	// not yet forwarded (from workspace.discover-ports).
	if len(m.sidebarDiscovered) > 0 {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Detected in workspace:"))
		b.WriteString("\n")
		for _, dp := range m.sidebarDiscovered {
			label := dp.Service
			if label == "" {
				label = dp.Protocol
			}
			var line string
			if label != "" {
				line = fmt.Sprintf("  · %d  %s", dp.RemotePort, label)
			} else {
				line = fmt.Sprintf("  · %d", dp.RemotePort)
			}
			b.WriteString(mutedStyle.Render(truncate(line, max(width-2, 8))))
			b.WriteString("\n")
		}
		b.WriteString(mutedStyle.Render("  [n] forward  [a] forward all"))
		b.WriteString("\n")
	}

	if focused && len(m.sidebarDiscovered) == 0 {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("[n] add  [x] del  [a] toggle"))
		b.WriteString("\n")
	}

	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}
