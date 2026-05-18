package components

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// BadgeVariant defines the visual style of a badge.
type BadgeVariant int

const (
	BadgeVariantNeutral BadgeVariant = iota
	BadgeVariantSuccess
	BadgeVariantWarning
	BadgeVariantError
	BadgeVariantAccent
)

// BadgeStyle renders a status badge/pill with the given text.
// Compact removes padding for a smaller inline badge.
func BadgeStyle(text string, variant BadgeVariant, compact bool) string {
	var color lipgloss.AdaptiveColor

	switch variant {
	case BadgeVariantSuccess:
		color = design.Success
	case BadgeVariantWarning:
		color = design.Warning
	case BadgeVariantError:
		color = design.Error
	case BadgeVariantAccent:
		color = design.Accent
	case BadgeVariantNeutral:
		color = design.Muted
	}

	style := lipgloss.NewStyle().
		Foreground(color).
		Background(design.SelBg)

	if !compact {
		style = style.Padding(0, 1)
	}

	return style.Render(text)
}

// BadgeDot renders a small colored dot indicator without text.
func BadgeDot(variant BadgeVariant) string {
	var color lipgloss.AdaptiveColor

	switch variant {
	case BadgeVariantSuccess:
		color = design.Success
	case BadgeVariantWarning:
		color = design.Warning
	case BadgeVariantError:
		color = design.Error
	case BadgeVariantAccent:
		color = design.Accent
	case BadgeVariantNeutral:
		color = design.Muted
	}

	return lipgloss.NewStyle().Foreground(color).Render("●")
}

// BadgeStatus renders a common status text badge (running, stopped, etc.).
func BadgeStatus(status string) string {
	switch status {
	case "running", "active", "online", "up":
		return BadgeDot(BadgeVariantSuccess) + " " + design.StatusOkStyle.Render(status)
	case "stopped", "inactive", "offline", "down":
		return BadgeDot(BadgeVariantNeutral) + " " + design.MutedStyle.Render(status)
	case "error", "failed", "crashed":
		return BadgeDot(BadgeVariantError) + " " + design.StatusErrStyle.Render(status)
	case "warning", "degraded":
		return BadgeDot(BadgeVariantWarning) + " " + design.WarningStyle.Render(status)
	default:
		return BadgeDot(BadgeVariantNeutral) + " " + design.MutedStyle.Render(status)
	}
}

// BadgeCount renders a count badge (e.g., notification count).
func BadgeCount(count int, maxDigits int) string {
	if count <= 0 {
		return ""
	}

	text := ""
	if maxDigits > 0 && count > 9 {
		text = "9+"
	} else {
		text = string(rune('0' + count))
	}

	return BadgeStyle(text, BadgeVariantAccent, false)
}
