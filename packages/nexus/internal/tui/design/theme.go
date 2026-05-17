package design

import "github.com/charmbracelet/lipgloss"

// Theme represents a complete visual theme.
type Theme struct {
	Name   string
	Colors ColorTokens
}

// ActiveTheme is the currently active theme. Defaults to ThemeDark.
var ActiveTheme = ThemeDark

// GetTheme returns the currently active theme.
func GetTheme() *Theme {
	return &ActiveTheme
}

// ThemeDark is the default dark theme (Tokyo Night inspired).
var ThemeDark = Theme{
	Name: "dark",
	Colors: ColorTokens{
		Bg:          lipgloss.Color("#1a1b26"),
		BgSubtle:    lipgloss.Color("#24283b"),
		BgSelected:  lipgloss.Color("#364a82"),
		Text:        lipgloss.Color("#c0caf5"),
		TextSubtle:  lipgloss.Color("#a9b1d6"),
		TextMuted:   lipgloss.Color("#565f89"),
		Accent:      lipgloss.Color("#7aa2f7"),
		Success:     lipgloss.Color("#9ece6a"),
		Warning:     lipgloss.Color("#e0af68"),
		Error:       lipgloss.Color("#f7768e"),
		Info:        lipgloss.Color("#7dcfff"),
		Border:      lipgloss.Color("#3b4261"),
		BorderFocus: lipgloss.Color("#7aa2f7"),
	},
}

// ThemeLight is the light theme.
var ThemeLight = Theme{
	Name: "light",
	Colors: ColorTokens{
		Bg:          lipgloss.Color("#fafafa"),
		BgSubtle:    lipgloss.Color("#f0f0f0"),
		BgSelected:  lipgloss.Color("#e0e7ff"),
		Text:        lipgloss.Color("#1e293b"),
		TextSubtle:  lipgloss.Color("#475569"),
		TextMuted:   lipgloss.Color("#94a3b8"),
		Accent:      lipgloss.Color("#6d28d9"),
		Success:     lipgloss.Color("#16a34a"),
		Warning:     lipgloss.Color("#d97706"),
		Error:       lipgloss.Color("#dc2626"),
		Info:        lipgloss.Color("#0284c7"),
		Border:      lipgloss.Color("#d1d5db"),
		BorderFocus: lipgloss.Color("#6d28d9"),
	},
}
