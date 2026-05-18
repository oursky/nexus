package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
)

// SpotlightSidebarConfig holds configuration for rendering the spotlight sidebar.
type SpotlightSidebarConfig struct {
	// Width is the sidebar width.
	Width int
	// Height is the sidebar height.
	Height int
	// Focused indicates whether the sidebar has focus.
	Focused bool
	// Forwards is the list of port forwards to display.
	Forwards []*spotlight.Forward
	// Sel is the selected forward index.
	Sel int
	// ConfirmRemove indicates whether we're confirming removal.
	ConfirmRemove bool
	// TunnelLive indicates whether the tunnel is live.
	TunnelLive bool
	// TunnelErr contains any tunnel error message.
	TunnelErr string
	// Discovered contains discovered ports in the workspace.
	Discovered []DiscoveredPort
}

// DiscoveredPort represents a discovered service port in the workspace.
type DiscoveredPort struct {
	Service    string
	Protocol   string
	RemotePort int
}

// Styles for spotlight sidebar.
var (
	sectionLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	accentStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("229"))
	statusOkStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	statusErrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	mutedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	separatorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	warningStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
)

// RenderSpotlightSidebar renders the right-side spotlight sidebar for the
// three-pane split layout. width/height are the inner content dimensions
// (already accounting for any surrounding border).
func RenderSpotlightSidebar(cfg SpotlightSidebarConfig) string {
	width := cfg.Width
	height := cfg.Height

	headerStyle := sectionLabelStyle.Bold(false)
	if cfg.Focused {
		headerStyle = accentStyle
	}

	// Count active forwards.
	activeCount := 0
	for _, f := range cfg.Forwards {
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
		if cfg.TunnelLive {
			badge += "  " + statusOkStyle.Render("✓ tunneled")
		} else if cfg.TunnelErr == "" {
			badge += "  " + statusErrStyle.Render("✗ starting")
		}
		b.WriteString(badge)
		b.WriteString("\n")
	}
	b.WriteString(separatorStyle.Render(strings.Repeat("─", max(width-1, 1))))
	b.WriteString("\n")

	if len(cfg.Forwards) == 0 {
		b.WriteString(mutedStyle.Render("No forwards"))
		b.WriteString("\n")
	} else {
		for i, f := range cfg.Forwards {
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
			if i == cfg.Sel && cfg.Focused && cfg.ConfirmRemove {
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
				if cfg.TunnelLive {
					tunnelSuffix = " " + statusOkStyle.Render(fmt.Sprintf("✓ :%d", f.LocalPort))
				} else {
					tunnelSuffix = " " + statusErrStyle.Render("✗")
				}
			}

			row := truncate(portStr, max(width-5, 6))
			var rendered string
			if i == cfg.Sel && cfg.Focused {
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
	if cfg.TunnelErr != "" {
		b.WriteString("\n")
		errLine := statusErrStyle.Render("✗ " + cfg.TunnelErr)
		b.WriteString(errLine)
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("toggle (a) to retry"))
		b.WriteString("\n")
	}

	// Discovered ports section.
	if len(cfg.Discovered) > 0 {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Detected in workspace:"))
		b.WriteString("\n")
		for _, dp := range cfg.Discovered {
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
	if cfg.Focused {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("n add · x remove · a toggle all"))
		b.WriteString("\n")
	}

	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}

// SpotlightPanelConfig holds configuration for rendering the spotlight panel.
type SpotlightPanelConfig struct {
	Width    int
	Forwards []*spotlight.Forward
	Sel      int
}

// RenderSpotlightPanel renders the spotlight forwards panel.
func RenderSpotlightPanel(cfg SpotlightPanelConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Spotlight forwards"))
	if len(cfg.Forwards) == 0 {
		b.WriteString(mutedStyle.Render("No active forwards. Press n to add (remote port).\n"))
	} else {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render("j/k select · n new · x remove forward"))
		for i, f := range cfg.Forwards {
			if f == nil {
				continue
			}
			// Show from user perspective: local port they connect to.
			var portPart string
			if f.LocalPort == f.RemotePort {
				portPart = fmt.Sprintf("localhost:%d", f.LocalPort)
			} else {
				portPart = fmt.Sprintf("localhost:%d → :%d", f.LocalPort, f.RemotePort)
			}
			stateStr := string(f.State)
			if i == cfg.Sel {
				icon := "○"
				if f.State == spotlight.ForwardStateActive {
					icon = "●"
				}
				sel := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5"))
				fmt.Fprintf(&b, "%s\n", sel.Render(fmt.Sprintf("› %s  %s  (%s)", icon, portPart, stateStr)))
			} else {
				var icon string
				if f.State == spotlight.ForwardStateActive {
					icon = statusOkStyle.Render("●")
				} else {
					icon = mutedStyle.Render("○")
				}
				fmt.Fprintf(&b, "  %s  %s  %s\n", icon, portPart, mutedStyle.Render("("+stateStr+")"))
			}
		}
	}
	return lipgloss.NewStyle().MaxWidth(cfg.Width).Render(b.String())
}
