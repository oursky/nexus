package layouts

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// CenteredLayout renders content centered within the available space.
//
// This layout is used for:
// - Modal dialogs (confirmations, input forms)
// - Empty states (no workspaces, no spotlight forwards)
// - First-run onramp screens
//
// Parameters:
//   - content: the content to center (already rendered with borders/styling)
//   - availWidth: available width for centering (typically terminal width - padding)
//   - availHeight: available height for centering (typically terminal height - header/footer)
//
// Returns the content centered within the available space.
func CenteredLayout(content string, availWidth, availHeight int) string {
	return lipgloss.Place(availWidth, availHeight, lipgloss.Center, lipgloss.Center, content)
}

// Modal renders a centered modal dialog with title, body, and action hints.
//
// Parameters:
//   - title: modal title (e.g., "Delete workspace?")
//   - body: modal body content (can be multi-line)
//   - actions: action hints to display at the bottom (e.g., "y confirm · n cancel")
//   - width: modal content width (excluding border)
//   - focused: whether the modal has keyboard focus
//
// Returns the rendered modal ready for display.
func Modal(title, body string, actions []string, width int, focused bool) string {
	var parts []string
	parts = append(parts, design.TitleStyle.Render(title))
	if body != "" {
		parts = append(parts, "", body)
	}
	if len(actions) > 0 {
		parts = append(parts, "")
		for i, action := range actions {
			parts = append(parts, design.MutedStyle.Render(action))
			if i < len(actions)-1 {
				parts[len(parts)-1] += " · "
			}
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	borderColor := design.Border
	if focused {
		borderColor = design.Accent
	}

	modalWidth := width
	if modalWidth < 20 {
		modalWidth = 20
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(modalWidth).
		Render(content)

	return modalBox(modalWidth+4, box) // +4 for padding
}

// InputModal renders a centered modal with a text input field.
//
// Parameters:
//   - title: modal title (e.g., "Forward remote port")
//   - label: label for the input field (e.g., "Remote port:")
//   - input: rendered text input component
//   - hint: hint text below the input (e.g., validation or help text)
//   - actions: action hints to display at the bottom
//   - width: modal content width (excluding border)
//
// Returns the rendered modal ready for display.
func InputModal(title, label, input, hint string, actions []string, width int) string {
	var parts []string
	parts = append(parts, design.TitleStyle.Render(title))
	if label != "" {
		parts = append(parts, "", label)
	}
	parts = append(parts, input)
	if hint != "" {
		parts = append(parts, "", hint)
	}
	if len(actions) > 0 {
		parts = append(parts, "")
		for i, action := range actions {
			parts = append(parts, design.MutedStyle.Render(action))
			if i < len(actions)-1 {
				parts[len(parts)-1] += " · "
			}
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	modalWidth := width
	if modalWidth < 20 {
		modalWidth = 20
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(design.Accent).
		Padding(1, 2).
		Width(modalWidth).
		Render(content)

	return modalBox(modalWidth+4, box) // +4 for padding
}

// EmptyState renders a centered empty state message.
//
// Parameters:
//   - title: empty state title (e.g., "No workspaces")
//   - subtitle: optional subtitle (e.g., "Press n to create a workspace")
//   - width: available width
//   - height: available height
//
// Returns the rendered empty state.
func EmptyState(title, subtitle string, width, height int) string {
	var parts []string
	parts = append(parts, design.TitleStyle.Render(title))
	if subtitle != "" {
		parts = append(parts, "", design.MutedStyle.Render(subtitle))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

// ConfirmDialog renders a confirmation dialog with a message and y/n options.
//
// Parameters:
//   - message: confirmation message (e.g., "Delete workspace my-workspace?")
//   - width: dialog width
//
// Returns the rendered confirmation dialog.
func ConfirmDialog(message string, width int) string {
	return design.WarningStyle.Render(message)
}

// modalBox wraps modal content in a centered box with proper dimensions.
func modalBox(contentWidth int, content string) string {
	return content
}

// ModalWidth calculates the appropriate modal width based on terminal width.
func ModalWidth(termWidth int) int {
	innerWidth := max(termWidth-4, 20)
	if innerWidth > 60 {
		return 60
	}
	return innerWidth
}

// ModalHeight calculates the appropriate modal height based on terminal height.
func ModalHeight(termHeight int) int {
	// Reserve space for header (2 lines) + footer (3 lines) + padding (4 lines)
	innerHeight := max(termHeight-9, 10)
	if innerHeight > 20 {
		return 20
	}
	return innerHeight
}
