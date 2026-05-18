package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// ButtonVariant defines the visual style of a button.
type ButtonVariant int

const (
	ButtonVariantPrimary ButtonVariant = iota
	ButtonVariantSecondary
	ButtonVariantDanger
)

// ButtonStyle returns the rendered string for a button with given text and variant.
// Width controls the button's minimum width (0 for auto-size).
// Focused indicates if the button has keyboard focus.
func ButtonStyle(text string, variant ButtonVariant, width int, focused bool) string {
	baseStyle := lipgloss.NewStyle().
		Padding(0, 2).
		Height(1)

	var style lipgloss.Style
	switch variant {
	case ButtonVariantPrimary:
		if focused {
			style = baseStyle.
				Foreground(lipgloss.Color("#ffffff")).
				Background(design.Accent).
				Bold(true)
		} else {
			style = baseStyle.
				Foreground(design.Text).
				Background(design.Accent)
		}
	case ButtonVariantSecondary:
		if focused {
			style = baseStyle.
				Foreground(design.Text).
				Background(design.Border).
				Bold(true)
		} else {
			style = baseStyle.
				Foreground(design.Text).
				Background(design.Border)
		}
	case ButtonVariantDanger:
		if focused {
			style = baseStyle.
				Foreground(lipgloss.Color("#ffffff")).
				Background(design.Error).
				Bold(true)
		} else {
			style = baseStyle.
				Foreground(design.Text).
				Background(design.Error)
		}
	}

	if width > 0 {
		style = style.Width(width)
	}

	rendered := style.Render(text)
	if width > 0 && lipgloss.Width(rendered) < width {
		rendered = lipgloss.NewStyle().Width(width).Render(rendered)
	}

	return rendered
}

// ButtonLabel returns just the styled text for use in more complex layouts.
func ButtonLabel(text string, variant ButtonVariant, focused bool) string {
	style := lipgloss.NewStyle().Bold(true)
	switch variant {
	case ButtonVariantPrimary:
		if focused {
			style = style.Foreground(design.Accent)
		} else {
			style = style.Foreground(design.Accent)
		}
	case ButtonVariantSecondary:
		if focused {
			style = style.Foreground(design.Text)
		} else {
			style = style.Foreground(design.Subtext)
		}
	case ButtonVariantDanger:
		if focused {
			style = style.Foreground(design.Error)
		} else {
			style = style.Foreground(design.Error)
		}
	}
	return style.Render(text)
}

// ButtonsRow renders multiple buttons in a horizontal row with spacing.
func ButtonsRow(buttons []string, variant ButtonVariant, selectedIndex int, gap string) string {
	var rendered []string
	for i, text := range buttons {
		btn := ButtonStyle(text, variant, 0, i == selectedIndex)
		rendered = append(rendered, btn)
	}
	return strings.Join(rendered, gap)
}
