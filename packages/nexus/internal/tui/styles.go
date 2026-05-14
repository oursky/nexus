package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#EE6FF8"))

	statusOkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	statusErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5F87"))

	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	detailKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A8A8A8"))

	detailValStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E8E8E8"))

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFB86C"))

	// Tab bar — active tab uses accent colour; inactive tabs are muted.
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#EE6FF8"))

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#626262"))

	tabSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3A3A3A"))
)
