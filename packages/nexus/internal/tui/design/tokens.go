package design

import "github.com/charmbracelet/lipgloss"

// Design tokens for the TUI theme.
var (
	Accent  = lipgloss.AdaptiveColor{Dark: "#7c3aed", Light: "#6d28d9"}
	Success = lipgloss.AdaptiveColor{Dark: "#22c55e", Light: "#16a34a"}
	Warning = lipgloss.AdaptiveColor{Dark: "#f59e0b", Light: "#d97706"}
	Error   = lipgloss.AdaptiveColor{Dark: "#ef4444", Light: "#dc2626"}
	Muted   = lipgloss.AdaptiveColor{Dark: "#6b7280", Light: "#9ca3af"}
	Text    = lipgloss.AdaptiveColor{Dark: "#e2e8f0", Light: "#1e293b"}
	Subtext = lipgloss.AdaptiveColor{Dark: "#a1aaba", Light: "#64748b"}
	Border  = lipgloss.AdaptiveColor{Dark: "#333333", Light: "#d1d5db"}
	SelBg   = lipgloss.AdaptiveColor{Dark: "#1e1b4b", Light: "#ede9fe"}

	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Text)

	StatusOkStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Success)

	StatusErrStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Error)

	MutedStyle = lipgloss.NewStyle().
			Foreground(Muted)

	DetailKeyStyle = lipgloss.NewStyle().
			Foreground(Subtext)

	DetailValStyle = lipgloss.NewStyle().
			Foreground(Text)

	WarningStyle = lipgloss.NewStyle().
			Foreground(Warning)

	AccentStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Accent)

	SeparatorStyle = lipgloss.NewStyle().
			Foreground(Border)

	SectionLabelStyle = lipgloss.NewStyle().
				Foreground(Muted)

	ActiveTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Accent)

	InactiveTabStyle = lipgloss.NewStyle().
				Foreground(Muted)

	TabSepStyle = lipgloss.NewStyle().
			Foreground(Border)
)
