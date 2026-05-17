package components

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

type ToastLevel int

const (
	ToastSuccess ToastLevel = iota
	ToastError
	ToastWarning
	ToastInfo
)

type Toast struct {
	Level   ToastLevel
	Message string
	Time    time.Time
}

type ShowToastMsg struct {
	Level   ToastLevel
	Message string
}

type ToastTimeoutMsg time.Time

func ShowToast(level ToastLevel, message string) tea.Cmd {
	return func() tea.Msg {
		return ShowToastMsg{Level: level, Message: message}
	}
}

func ToastDismissAfter(toast Toast, duration time.Duration) tea.Cmd {
	return tea.Tick(duration, func(_ time.Time) tea.Msg {
		return ToastTimeoutMsg(toast.Time)
	})
}

func (t *Toast) View() string {
	c := design.ActiveTheme.Colors

	var style lipgloss.Style
	switch t.Level {
	case ToastSuccess:
		style = lipgloss.NewStyle().
			Foreground(c.Success).
			Background(c.BgSubtle).
			Padding(0, design.SpaceSM).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c.Success)
	case ToastError:
		style = lipgloss.NewStyle().
			Foreground(c.Error).
			Background(c.BgSubtle).
			Padding(0, design.SpaceSM).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c.Error)
	case ToastWarning:
		style = lipgloss.NewStyle().
			Foreground(c.Warning).
			Background(c.BgSubtle).
			Padding(0, design.SpaceSM).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c.Warning)
	default: // ToastInfo
		style = lipgloss.NewStyle().
			Foreground(c.Info).
			Background(c.BgSubtle).
			Padding(0, design.SpaceSM).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c.Info)
	}

	return style.Render(t.Message)
}
