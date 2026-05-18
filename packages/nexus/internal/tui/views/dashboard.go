package views

import (
	"github.com/charmbracelet/lipgloss"
)

// SplitLayoutConfig holds configuration for rendering the split-pane layout.
type SplitLayoutConfig struct {
	// IsThreePane indicates whether to render the three-pane layout (workspace list | PTY | spotlight).
	IsThreePane bool
	// Width is the total terminal width.
	Width int
	// Height is the total terminal height.
	Height int
	// PTYFocused indicates whether the PTY pane has focus.
	PTYFocused bool
	// SidebarFocused indicates whether the spotlight sidebar has focus.
	SidebarFocused bool
	// LeftBorderColor is the border color for the left pane.
	LeftBorderColor lipgloss.TerminalColor
	// CenterBorderColor is the border color for the center pane.
	CenterBorderColor lipgloss.TerminalColor
	// RightBorderColor is the border color for the right pane.
	RightBorderColor lipgloss.TerminalColor
}

// PaneDimensions returns the width of each pane in the split layout.
// Two-pane  (90–109 cols): workspace list (28%) | PTY (72%).
// Three-pane (≥110 cols):  workspace list (28%) | PTY (≈50%) | spotlight (22%).
func (c SplitLayoutConfig) PaneDimensions() (left, center, right int) {
	// Two-pane: list gets 28%, PTY gets the rest.
	// Three-pane: list 28%, PTY 50%, spotlight 22%.
	if c.IsThreePane {
		return c.Width * 28 / 100, c.Width * 50 / 100, c.Width * 22 / 100
	}
	return c.Width * 28 / 100, c.Width - (c.Width * 28 / 100), 0
}

// Dashboard renders the default workspace list + PTY split view.
// This is the main dashboard view when no overlay is active.
//
// The function signature and implementation will be completed by extracting
// the renderSplitLayout function from the parent tui package.
type Dashboard struct {
	config SplitLayoutConfig
}

// NewDashboard creates a new dashboard view with the given configuration.
func NewDashboard(cfg SplitLayoutConfig) *Dashboard {
	return &Dashboard{config: cfg}
}
