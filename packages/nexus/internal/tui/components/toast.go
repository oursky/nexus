package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// ToastLevel defines the severity/type of toast notification.
type ToastLevel int

const (
	ToastLevelInfo ToastLevel = iota
	ToastLevelSuccess
	ToastLevelWarning
	ToastLevelError
)

// ToastData represents a toast notification's content and metadata.
type ToastData struct {
	Message string
	Level   ToastLevel
	// DismissMs is the auto-dismiss duration in milliseconds (0 for no auto-dismiss)
	DismissMs int
}

// ToastStyle renders a toast notification with appropriate styling.
// Width controls the toast's maximum width (0 for no constraint).
func ToastStyle(data ToastData, width int) string {
	var icon string
	var iconStyle lipgloss.Style
	var msgStyle lipgloss.Style

	switch data.Level {
	case ToastLevelSuccess:
		icon = "✓"
		iconStyle = lipgloss.NewStyle().Foreground(design.Success).Bold(true)
		msgStyle = lipgloss.NewStyle().Foreground(design.Text)
	case ToastLevelError:
		icon = "✕"
		iconStyle = lipgloss.NewStyle().Foreground(design.Error).Bold(true)
		msgStyle = lipgloss.NewStyle().Foreground(design.Text)
	case ToastLevelWarning:
		icon = "⚠"
		iconStyle = lipgloss.NewStyle().Foreground(design.Warning).Bold(true)
		msgStyle = lipgloss.NewStyle().Foreground(design.Text)
	case ToastLevelInfo:
		icon = "ℹ"
		iconStyle = lipgloss.NewStyle().Foreground(design.Accent).Bold(true)
		msgStyle = lipgloss.NewStyle().Foreground(design.Text)
	}

	content := strings.Join([]string{
		iconStyle.Render(icon),
		msgStyle.Render(data.Message),
	}, " ")

	borderStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(design.Border)

	if width > 0 {
		content = lipgloss.NewStyle().Width(width - 4).Render(content)
		content = strings.TrimSpace(content)
	}

	return borderStyle.Render(content)
}

// ToastStack renders multiple toasts stacked vertically.
// MaxHeight limits the stack's height (0 for no limit).
func ToastStack(toasts []ToastData, maxWidth, maxHeight int) string {
	if len(toasts) == 0 {
		return ""
	}

	var rendered []string
	for _, toast := range toasts {
		rendered = append(rendered, ToastStyle(toast, maxWidth))
	}

	stack := strings.Join(rendered, "\n")

	if maxHeight > 0 && lipgloss.Height(stack) > maxHeight {
		visible := toasts[:maxHeight]
		var trimmed []string
		for _, toast := range visible {
			trimmed = append(trimmed, ToastStyle(toast, maxWidth))
		}
		stack = strings.Join(trimmed, "\n")
	}

	return stack
}

// ToastInline renders a compact inline toast (for use in status bars).
func ToastInline(message string, level ToastLevel) string {
	var style lipgloss.Style
	var icon string

	switch level {
	case ToastLevelSuccess:
		icon = "✓"
		style = design.StatusOkStyle
	case ToastLevelError:
		icon = "✕"
		style = design.StatusErrStyle
	case ToastLevelWarning:
		icon = "!"
		style = design.WarningStyle
	case ToastLevelInfo:
		icon = "i"
		style = design.AccentStyle
	}

	return style.Render(icon + " " + message)
}
