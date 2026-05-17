package layouts

import (
	"github.com/charmbracelet/lipgloss"
)

// FullscreenLayout renders content filling the entire screen area.
func FullscreenLayout(content string, width, height int) string {
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}

	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}
