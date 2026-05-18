package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// ModalStyle renders a modal dialog overlay with the given content.
// Width and height specify the modal dimensions (0 for auto-size).
// Centered content is automatically positioned.
func ModalStyle(title string, body string, width, height int) string {
	border := lipgloss.RoundedBorder()
	borderStyle := lipgloss.NewStyle().
		Border(border).
		BorderForeground(design.Accent).
		Padding(1, 2)

	if width > 0 {
		borderStyle = borderStyle.Width(width)
	}
	if height > 0 {
		borderStyle = borderStyle.Height(height)
	}

	var parts []string

	if title != "" {
		titleRendered := design.TitleStyle.Render(title)
		parts = append(parts, titleRendered)
	}

	if body != "" {
		parts = append(parts, body)
	}

	content := strings.Join(parts, "\n\n")

	return borderStyle.Render(content)
}

// ModalWithButtons renders a modal with action buttons at the bottom.
func ModalWithButtons(title, body string, buttons []string, primaryIndex int, width, height int) string {
	border := lipgloss.RoundedBorder()
	borderStyle := lipgloss.NewStyle().
		Border(border).
		BorderForeground(design.Accent).
		Padding(1, 2)

	var parts []string

	if title != "" {
		parts = append(parts, design.TitleStyle.Render(title))
	}

	if body != "" {
		parts = append(parts, body)
	}

	if len(buttons) > 0 {
		var buttonParts []string
		for i, btn := range buttons {
			variant := ButtonVariantSecondary
			if i == primaryIndex {
				variant = ButtonVariantPrimary
			}
			buttonParts = append(buttonParts, ButtonStyle(btn, variant, 0, false))
		}
		parts = append(parts, strings.Join(buttonParts, "  "))
	}

	content := strings.Join(parts, "\n\n")

	if width > 0 {
		borderStyle = borderStyle.Width(width)
	}
	if height > 0 {
		borderStyle = borderStyle.Height(height)
	}

	return borderStyle.Render(content)
}

// ModalConfirm renders a confirmation dialog with Yes/No buttons.
func ModalConfirm(message string) string {
	return ModalWithButtons(
		"Confirm",
		message,
		[]string{"Yes", "No"},
		0, // Yes is primary
		0, 0,
	)
}

// ModalAlert renders an alert/warning dialog.
func ModalAlert(title, message string) string {
	border := lipgloss.RoundedBorder()
	borderStyle := lipgloss.NewStyle().
		Border(border).
		BorderForeground(design.Warning).
		Padding(1, 2)

	content := strings.Join([]string{
		design.WarningStyle.Render(title),
		message,
	}, "\n\n")

	return borderStyle.Render(content)
}

// ModalError renders an error dialog.
func ModalError(message string) string {
	border := lipgloss.RoundedBorder()
	borderStyle := lipgloss.NewStyle().
		Border(border).
		BorderForeground(design.Error).
		Padding(1, 2)

	content := strings.Join([]string{
		design.StatusErrStyle.Render("Error"),
		message,
	}, "\n\n")

	return borderStyle.Render(content)
}

// Overlay creates a dimmed background effect (useful behind modals).
func Overlay(content string, width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content, lipgloss.WithWhitespaceChars(" "))
}
