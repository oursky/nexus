package tui

import "github.com/charmbracelet/lipgloss"

// Ralph-tui inspired dark theme colour palette.
var (
	colorAccent  = lipgloss.AdaptiveColor{Dark: "#7c3aed", Light: "#6d28d9"}
	colorSuccess = lipgloss.AdaptiveColor{Dark: "#22c55e", Light: "#16a34a"}
	colorWarning = lipgloss.AdaptiveColor{Dark: "#f59e0b", Light: "#d97706"}
	colorError   = lipgloss.AdaptiveColor{Dark: "#ef4444", Light: "#dc2626"}
	colorMuted   = lipgloss.AdaptiveColor{Dark: "#6b7280", Light: "#9ca3af"}
	colorText    = lipgloss.AdaptiveColor{Dark: "#e2e8f0", Light: "#1e293b"}
	colorSubtext = lipgloss.AdaptiveColor{Dark: "#a1aaba", Light: "#64748b"}
	colorBorder  = lipgloss.AdaptiveColor{Dark: "#333333", Light: "#d1d5db"}
	colorSelBg   = lipgloss.AdaptiveColor{Dark: "#1e1b4b", Light: "#ede9fe"}

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText)

	statusOkStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSuccess)

	statusErrStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorError)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	detailKeyStyle = lipgloss.NewStyle().
			Foreground(colorSubtext)

	detailValStyle = lipgloss.NewStyle().
			Foreground(colorText)

	warningStyle = lipgloss.NewStyle().
			Foreground(colorWarning)

	accentStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	separatorStyle = lipgloss.NewStyle().
			Foreground(colorBorder)

	sectionLabelStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	// Tab bar — active tab uses accent colour; inactive tabs are muted.
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	tabSepStyle = lipgloss.NewStyle().
			Foreground(colorBorder)
)
