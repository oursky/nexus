package layouts

import (
	"github.com/charmbracelet/lipgloss"
)

// SplitLayout renders a left sidebar and right content pane.
// Sidebar width can be configured; content fills remaining width.
func SplitLayout(sidebar, content string, sidebarWidth, totalWidth, height int) string {
	if totalWidth <= 0 {
		totalWidth = 100 // default
	}
	if sidebarWidth <= 0 {
		sidebarWidth = 30 // default sidebar width
	}

	contentWidth := totalWidth - sidebarWidth
	if contentWidth < 1 {
		contentWidth = 1
	}

	sidebarStyled := lipgloss.NewStyle().Width(sidebarWidth).Height(height).Render(sidebar)
	contentStyled := lipgloss.NewStyle().Width(contentWidth).Height(height).Render(content)

	return lipgloss.JoinHorizontal(lipgloss.Top, sidebarStyled, contentStyled)
}
