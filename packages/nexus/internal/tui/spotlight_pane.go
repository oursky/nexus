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

	// Active count badge and tunnel status.
	if activeCount > 0 {
		badge := statusOkStyle.Render(fmt.Sprintf("● %d active", activeCount))
		if m.sidebarTunnelLive {
			badge += "  " + statusOkStyle.Render("✓ tunneled")
		} else if m.sidebarTunnelErr == "" {
			badge += "  " + statusErrStyle.Render("✗ starting")
		}
		b.WriteString(badge)
		b.WriteString("\n")
	}
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

			portStr := fmt.Sprintf("%d", f.LocalPort)

			// Row confirm state: selected row shows "remove? [y/n]".
			if i == m.sidebarSel && focused && m.sidebarConfirmRemove {
				row := lipgloss.NewStyle().
					Foreground(lipgloss.Color("#FFFDF5")).
					Render("›"+indicator+" "+portStr) +
					"  " + warningStyle.Render("remove? [y/n]")
				b.WriteString(row)
				b.WriteString("\n")
				continue
			}

			// Tunnel status suffix for active forwards.
			var tunnelSuffix string
			if f.State == spotlight.ForwardStateActive {
				if m.sidebarTunnelLive {
					tunnelSuffix = " " + statusOkStyle.Render(fmt.Sprintf("✓ :%d", f.LocalPort))
				} else {
					tunnelSuffix = " " + statusErrStyle.Render("✗")
				}
			}

			row := truncate(portStr, max(width-5, 6))
			var rendered string
			if i == m.sidebarSel && focused {
				rendered = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#FFFDF5")).
					Render("›"+indicator+" "+row) + tunnelSuffix
			} else {
				rendered = "  " + indicator + " " + row + tunnelSuffix
			}
			b.WriteString(rendered)
			b.WriteString("\n")
		}
	}

	// Raw tunnel error — displayed verbatim, wrapped to sidebar width.
	if m.sidebarTunnelErr != "" {
		b.WriteString("\n")
		errLine := statusErrStyle.Render("✗ " + m.sidebarTunnelErr)
		b.WriteString(errLine)
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("toggle (a) to retry"))
		b.WriteString("\n")
	}

	// Discovered ports section.
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
	}

	// Key hints — always visible when focused.
	if focused {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("n add · x remove · a toggle all"))
		b.WriteString("\n")
	}

	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}
