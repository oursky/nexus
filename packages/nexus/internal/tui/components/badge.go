package components

import (
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

type BadgeStyle int

const (
	BadgeDefault BadgeStyle = iota
	BadgeSuccess
	BadgeWarning
	BadgeError
	BadgeAccent
	BadgeMuted
)

type Badge struct {
	Text  string
	Style BadgeStyle
}

func (b *Badge) View() string {
	c := design.ActiveTheme.Colors

	var fg, bg lipgloss.TerminalColor
	switch b.Style {
	case BadgeSuccess:
		fg = c.Bg
		bg = c.Success
	case BadgeWarning:
		fg = c.Bg
		bg = c.Warning
	case BadgeError:
		fg = c.Bg
		bg = c.Error
	case BadgeAccent:
		fg = c.Bg
		bg = c.Accent
	case BadgeMuted:
		fg = c.TextMuted
		bg = c.BgSubtle
	default:
		fg = c.Text
		bg = c.BgSubtle
	}

	style := lipgloss.NewStyle().
		Foreground(fg).
		Background(bg).
		Padding(0, 1)

	return style.Render(b.Text)
}
