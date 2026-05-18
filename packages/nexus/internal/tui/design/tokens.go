package design

import "github.com/charmbracelet/lipgloss"

// ColorTokens holds all semantic color tokens for a theme.
type ColorTokens struct {
	// Surfaces
	Bg         lipgloss.Color
	BgSubtle   lipgloss.Color
	BgSelected lipgloss.Color
	BgOverlay  lipgloss.Color

	// Text
	Text       lipgloss.Color
	TextSubtle lipgloss.Color
	TextMuted  lipgloss.Color

	// Semantic
	Accent  lipgloss.Color
	Success lipgloss.Color
	Warning lipgloss.Color
	Error   lipgloss.Color
	Info    lipgloss.Color

	// Borders
	Border      lipgloss.Color
	BorderFocus lipgloss.Color
}

// Spacing scale (in characters). Use these for all padding/margin.
const (
	SpaceXS = 1
	SpaceSM = 2
	SpaceMD = 4
	SpaceLG = 6
	SpaceXL = 8
)
