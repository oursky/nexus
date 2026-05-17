package components

import (
	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

type ButtonState int

const (
	ButtonNormal ButtonState = iota
	ButtonHover
	ButtonDisabled
	ButtonActive
)

type Button struct {
	Label   string
	State   ButtonState
	Width   int
	Focused bool
}

func (b *Button) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter, tea.KeySpace:
			if b.State != ButtonDisabled {
				return func() tea.Msg { return ButtonClickedMsg{Label: b.Label} }
			}
		}
	}
	return nil
}

func (b *Button) View() string {
	t := design.ActiveTheme.Colors

	var style lipgloss.Style
	switch b.State {
	case ButtonActive:
		style = lipgloss.NewStyle().
			Bold(true).
			Foreground(t.Bg).
			Background(t.Accent).
			Padding(0, design.SpaceSM)
	case ButtonHover:
		style = lipgloss.NewStyle().
			Foreground(t.Text).
			Background(t.BgSelected).
			Padding(0, design.SpaceSM)
	case ButtonDisabled:
		style = lipgloss.NewStyle().
			Foreground(t.TextMuted).
			Padding(0, design.SpaceSM)
	default:
		style = lipgloss.NewStyle().
			Foreground(t.Text).
			Padding(0, design.SpaceSM)
	}

	if b.Focused {
		style = style.Border(lipgloss.RoundedBorder()).BorderForeground(t.BorderFocus)
	}

	return style.Render(b.Label)
}

type ButtonClickedMsg struct {
	Label string
}
