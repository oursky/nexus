package layouts

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// FullscreenLayout renders content in a fullscreen overlay.
//
// This layout is used for:
// - PTY fullscreen shell (t key in dashboard)
// - Detail views (workspace detail, help overlay)
// - Spotlight/sync panels on narrow terminals (< 110 cols)
//
// Parameters:
//   - content: the content to render (already rendered/styled)
//   - width: available width
//   - height: available height
//   - showBorder: whether to show a border around the content
//
// Returns the rendered fullscreen layout.
func FullscreenLayout(content string, width, height int, showBorder bool) string {
	if showBorder {
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(design.Accent).
			Padding(1, 2).
			Width(max(width-6, 20)).  // -6 for border + padding
			Height(max(height-4, 4)). // -4 for border top/bottom
			Render(content)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
	}

	// No border: fill available space
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(content)
}

// PTYFullscreen renders a PTY in fullscreen mode.
//
// Parameters:
//   - ptyContent: the PTY content to render
//   - width: PTY width in columns
//   - height: PTY height in rows
//
// Returns the rendered fullscreen PTY.
func PTYFullscreen(ptyContent string, width, height int) string {
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(ptyContent)
}

// FullscreenPanel renders a fullscreen panel with header and footer.
//
// This is used for detail views, help overlay, and spotlight/sync panels
// on narrow terminals where they can't fit as sidebars.
//
// Parameters:
//   - title: panel title (e.g., "Workspace Detail", "Help")
//   - content: panel body content
//   - footer: optional footer with key hints
//   - width: available width
//   - height: available height
//
// Returns the rendered fullscreen panel.
func FullscreenPanel(title, content, footer string, width, height int) string {
	panelWidth := max(width-4, 20)
	panelHeight := max(height-2, 4) // -2 for top/bottom padding

	var parts []string
	parts = append(parts, design.TitleStyle.Render(title))
	if content != "" {
		parts = append(parts, "", content)
	}
	if footer != "" {
		parts = append(parts, "", design.MutedStyle.Render(footer))
	}

	panelContent := lipgloss.JoinVertical(lipgloss.Left, parts...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(design.Accent).
		Padding(1, 2).
		Width(panelWidth).
		Height(panelHeight).
		Render(panelContent)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// FullscreenScrollable renders a fullscreen panel with scrollable content.
//
// This is used for help overlay and detail views that may exceed
// the available height.
//
// Parameters:
//   - title: panel title
//   - viewportContent: scrollable viewport content
//   - footer: optional footer with pagination or key hints
//   - width: available width
//   - height: available height
//
// Returns the rendered fullscreen scrollable panel.
func FullscreenScrollable(title, viewportContent, footer string, width, height int) string {
	panelWidth := max(width-4, 20)
	panelHeight := max(height-2, 4)

	var parts []string
	parts = append(parts, design.TitleStyle.Render(title))
	parts = append(parts, viewportContent)
	if footer != "" {
		parts = append(parts, "", design.MutedStyle.Render(footer))
	}

	panelContent := lipgloss.JoinVertical(lipgloss.Left, parts...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(design.Accent).
		Padding(1, 2).
		Width(panelWidth).
		Height(panelHeight).
		Render(panelContent)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// OverlayDimensions returns the appropriate dimensions for an overlay
// based on the current terminal size.
//
// Parameters:
//   - termWidth: terminal width
//   - termHeight: terminal height
//   - maxWidthRatio: maximum width as ratio of terminal width (0.0-1.0)
//   - maxHeightRatio: maximum height as ratio of terminal height (0.0-1.0)
//
// Returns (width, height) for the overlay.
func OverlayDimensions(termWidth, termHeight int, maxWidthRatio, maxHeightRatio float64) (int, int) {
	width := int(float64(termWidth) * maxWidthRatio)
	height := int(float64(termHeight) * maxHeightRatio)

	// Ensure minimum dimensions
	if width < 40 {
		width = min(40, termWidth-4)
	}
	if height < 10 {
		height = min(10, termHeight-4)
	}

	return width, height
}

// AvailableBodyHeight calculates the available height for body content
// given the terminal height and whether tabs are shown.
//
// Parameters:
//   - termHeight: terminal height
//   - tabBarShown: whether the tab bar is visible
//
// Returns the available height for body content.
func AvailableBodyHeight(termHeight int, tabBarShown bool) int {
	// Overhead: 2 header + tabBarHeight + 2 spacers + 3 footer = 7 + tabBarHeight
	tabBarHeight := 0
	if tabBarShown {
		tabBarHeight = 2 // 1 separator line + 1 tab content line
	}
	return max(termHeight-7-tabBarHeight, 1)
}
