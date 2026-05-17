package design

import "github.com/charmbracelet/lipgloss"

// Style variables are refreshed from ActiveTheme when UpdateStyles() is called.
var (
	// Typography
	Title   lipgloss.Style
	Body    lipgloss.Style
	Caption lipgloss.Style

	// Semantic
	AccentStyle  lipgloss.Style
	SuccessStyle lipgloss.Style
	WarningStyle lipgloss.Style
	ErrorStyle   lipgloss.Style
	MutedStyle   lipgloss.Style

	// Borders
	BorderStyle        lipgloss.Style
	FocusedBorderStyle lipgloss.Style
)

// Styles holds all design system styles.
type Styles struct {
	Title, Body, Caption lipgloss.Style
	AccentStyle, SuccessStyle, WarningStyle, ErrorStyle, MutedStyle lipgloss.Style
	BorderStyle, FocusedBorderStyle lipgloss.Style
}

// GetStyles returns a snapshot of all current styles.
func GetStyles() *Styles {
	return &Styles{
		Title:             Title,
		Body:              Body,
		Caption:           Caption,
		AccentStyle:       AccentStyle,
		SuccessStyle:      SuccessStyle,
		WarningStyle:      WarningStyle,
		ErrorStyle:        ErrorStyle,
		MutedStyle:        MutedStyle,
		BorderStyle:       BorderStyle,
		FocusedBorderStyle: FocusedBorderStyle,
	}
}

// UpdateStyles refreshes all style variables from ActiveTheme.
func UpdateStyles() {
	t := ActiveTheme.Colors

	Title = lipgloss.NewStyle().Bold(true).Foreground(t.Text)
	Body = lipgloss.NewStyle().Foreground(t.Text)
	Caption = lipgloss.NewStyle().Foreground(t.TextMuted)

	AccentStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	SuccessStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Success)
	WarningStyle = lipgloss.NewStyle().Foreground(t.Warning)
	ErrorStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Error)
	MutedStyle = lipgloss.NewStyle().Foreground(t.TextMuted)

	BorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Border)
	FocusedBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.BorderFocus)
}

func init() {
	UpdateStyles()
}
