package layouts

import (
	"github.com/charmbracelet/lipgloss"
)

// CenteredLayout centers content within the available width and height.
func CenteredLayout(content string, width, height int) string {
	if width <= 0 {
		width = 60
	}
	if height <= 0 {
		height = 10
	}

	contentStyled := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(content)

	return contentStyled
}
