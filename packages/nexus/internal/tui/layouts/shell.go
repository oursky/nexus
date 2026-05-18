package layouts

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// ShellLayout renders the main shell layout with header, body, and footer.
//
// The shell layout is the primary container for the TUI, providing:
// - Header bar with app name, connection status, and tabs
// - Body area that contains the main content (passed as bodyContent)
// - Footer bar with separator, status line, and key hints
//
// Parameters:
//   - width: total terminal width
//   - height: total terminal height
//   - headerTop: top header line (e.g., "nexus ● connected")
//   - headerBottom: optional second header line (e.g., status messages)
//   - tabBar: optional tab bar content (empty string if no tabs)
//   - bodyContent: main body content to render
//   - footerHelp: context-sensitive key hints
//   - confirmLine: optional confirmation message (e.g., delete confirmation)
//
// Returns a rendered string ready to be displayed.
func ShellLayout(width, height int, headerTop, headerBottom, tabBar, bodyContent, footerHelp, confirmLine string) string {
	innerWidth := max(width-4, 20)

	// Build footer
	var footerParts []string
	sep := design.SeparatorStyle.Render(strings.Repeat("─", innerWidth))
	if confirmLine != "" {
		footerParts = append(footerParts, confirmLine)
	}
	footerParts = append(footerParts, footerHelp)
	footer := lipgloss.JoinVertical(lipgloss.Left, sep, strings.Join(footerParts, "\n"))

	// Calculate content height for sticky footer
	// Overhead: 2 header + optional tabBar + 2 spacers + 3 footer = 7 + tabBarHeight
	tabBarHeight := 0
	if tabBar != "" {
		tabBarHeight = 2 // 1 separator line + 1 tab content line
	}
	contentHeight := max(height-7-tabBarHeight, 1)

	// Render body with fixed height
	body := lipgloss.NewStyle().Height(contentHeight).Render(bodyContent)

	// Build the vertical stack
	rows := []string{headerTop}
	if headerBottom != "" {
		rows = append(rows, headerBottom)
	}
	if tabBar != "" {
		rows = append(rows, design.TabSepStyle.Render(strings.Repeat("─", innerWidth)))
		rows = append(rows, tabBar)
	}
	rows = append(rows, "", body, "", footer)

	// Apply outer padding (2 columns on each side)
	box := lipgloss.NewStyle().
		MaxWidth(width).
		Padding(0, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, rows...))

	return box
}

// HeaderLine constructs the top header line with app name and status.
//
// Parameters:
//   - status: connection status badge (e.g., "● connected", "● disconnected")
//
// Returns the rendered header line.
func HeaderLine(status string) string {
	return lipgloss.JoinHorizontal(lipgloss.Left,
		design.AccentStyle.Render("nexus"),
		design.MutedStyle.Render("  "),
		status,
	)
}

// TabSeparator renders the separator line above the tab bar.
func TabSeparator(width int) string {
	return design.TabSepStyle.Render(strings.Repeat("─", max(width-4, 20)))
}

// FooterSeparator renders the separator line above the footer.
func FooterSeparator(width int) string {
	innerWidth := max(width-4, 20)
	return design.SeparatorStyle.Render(strings.Repeat("─", innerWidth))
}
