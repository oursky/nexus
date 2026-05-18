package layouts

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// LayoutMode represents the current responsive layout mode.
type LayoutMode int

const (
	// LayoutSingleColumn is for terminals < 90 cols
	LayoutSingleColumn LayoutMode = iota
	// LayoutTwoPane is for terminals 90-109 cols
	LayoutTwoPane
	// LayoutThreePane is for terminals >= 110 cols
	LayoutThreePane
)

// DetectLayoutMode returns the appropriate layout mode for the given width.
func DetectLayoutMode(width int) LayoutMode {
	innerWidth := max(width-4, 24)
	if innerWidth >= 110 {
		return LayoutThreePane
	}
	if innerWidth >= 90 {
		return LayoutTwoPane
	}
	return LayoutSingleColumn
}

// PaneDimensions returns the (leftW, centerW, rightW) dimensions for the current layout.
//
//   - Three-pane (>= 110 cols): leftW=28%, rightW=22%, centerW≈50% (remainder minus 1 sep).
//     The left pane has a RoundedBorder; right pane has a RoundedBorder; a
//     single separator column sits between center and right pane.
//   - Two-pane   (>= 90 cols):  leftW=28%, centerW=remainder.
//     The left pane has a RoundedBorder; center pane has a left border only.
//   - Single-pane:              leftW=full, centerW=full, rightW=0.
//
// The widths are outer pane dimensions (including any border chars). Content
// inside a bordered pane is always 2 cols narrower (1 left + 1 right border).
func PaneDimensions(width int) (leftW, centerW, rightW int) {
	inner := max(width-4, 24)
	mode := DetectLayoutMode(width)

	switch mode {
	case LayoutThreePane:
		leftW = int(float64(inner) * 0.28)
		rightW = int(float64(inner) * 0.22)
		centerW = max(inner-leftW-rightW-1, 20) // 1 sep column between center and right; ≈50%
	case LayoutTwoPane:
		leftW = int(float64(inner) * 0.28)
		centerW = max(inner-leftW, 20) // no explicit sep; left border on center pane
		rightW = 0
	default: // LayoutSingleColumn
		leftW = inner
		centerW = inner
		rightW = 0
	}
	return
}

// PaneConfig holds the configuration for a single pane in the split layout.
type PaneConfig struct {
	// Content is the rendered content to display in the pane.
	Content string
	// Focused indicates whether this pane has keyboard focus.
	Focused bool
	// Border indicates whether this pane should have a border.
	Border bool
	// BorderStyle is the style to use for the border (if Border is true).
	BorderStyle lipgloss.TerminalColor
}

// SplitLayout renders a split-pane layout with configurable panes.
//
// Parameters:
//   - width: total terminal width
//   - height: total terminal height (minus header/footer overhead)
//   - left: left pane configuration
//   - center: center pane configuration
//   - right: right pane configuration (only used in three-pane mode)
//
// Returns the rendered split layout string.
func SplitLayout(width, height int, left, center, right PaneConfig) string {
	mode := DetectLayoutMode(width)
	leftW, centerW, rightW := PaneDimensions(width)
	bodyHeight := max(height, 4)

	switch mode {
	case LayoutThreePane:
		return renderThreePane(leftW, centerW, rightW, bodyHeight, left, center, right)
	case LayoutTwoPane:
		return renderTwoPane(leftW, centerW, bodyHeight, left, center)
	default:
		// Single column: just render left pane full width
		return renderSinglePane(leftW, bodyHeight, left)
	}
}

// renderThreePane renders the three-pane layout (workspace list | PTY | spotlight).
func renderThreePane(leftW, centerW, rightW, bodyHeight int, left, center, right PaneConfig) string {
	// Left pane inner dimensions (inside RoundedBorder).
	leftInnerW := max(leftW-2, 4)
	leftInnerH := max(bodyHeight-2, 2)

	leftPane := lipgloss.NewStyle().
		Width(leftInnerW).
		Height(leftInnerH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(left.BorderStyle).
		Render(left.Content)

	// Center pane content (no left border in three-pane mode).
	centerPane := lipgloss.NewStyle().
		Width(centerW).
		Height(bodyHeight).
		Render(center.Content)

	// Separator between center and right pane.
	sep := lipgloss.NewStyle().
		Foreground(right.BorderStyle).
		Render("│")

	// Right pane inner dimensions (inside RoundedBorder).
	rightInnerW := max(rightW-2, 4)
	rightInnerH := max(bodyHeight-2, 2)

	rightPane := lipgloss.NewStyle().
		Width(rightInnerW).
		Height(rightInnerH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(right.BorderStyle).
		Render(right.Content)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, centerPane, sep, rightPane)
}

// renderTwoPane renders the two-pane layout (workspace list | PTY).
func renderTwoPane(leftW, centerW, bodyHeight int, left, center PaneConfig) string {
	// Left pane inner dimensions (inside RoundedBorder).
	leftInnerW := max(leftW-2, 4)
	leftInnerH := max(bodyHeight-2, 2)

	leftPane := lipgloss.NewStyle().
		Width(leftInnerW).
		Height(leftInnerH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(left.BorderStyle).
		Render(left.Content)

	// Center pane has a left border only in two-pane mode.
	centerInnerW := max(centerW-1, 4) // -1 for left border
	centerPane := lipgloss.NewStyle().
		Width(centerInnerW).
		Height(bodyHeight).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(center.BorderStyle).
		Render(center.Content)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, centerPane)
}

// renderSinglePane renders a single full-width pane.
func renderSinglePane(width, height int, pane PaneConfig) string {
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(pane.Content)
}

// FocusedBorderColor returns the accent color when focused, border color otherwise.
func FocusedBorderColor(focused bool) lipgloss.TerminalColor {
	if focused {
		return design.Accent
	}
	return design.Border
}
