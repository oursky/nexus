package layouts

import (
	"github.com/charmbracelet/lipgloss"
)

// ShellLayout renders header, body, and footer in a classic shell layout.
// Header is at the top, footer at the bottom, body fills the middle.
func ShellLayout(header, body, footer string, width, height int) string {
	if height <= 0 {
		height = 20 // default minimum
	}

	// Calculate body height: total - header - footer
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, lipgloss.NewStyle().Height(bodyHeight).Width(width).Render(body), footer)
}
