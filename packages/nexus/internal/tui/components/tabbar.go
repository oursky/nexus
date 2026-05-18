package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

// TabData represents a single tab in the tab bar.
type TabData struct {
	ID    string
	Label string
}

// TabBarStyle renders a horizontal tab bar.
// ActiveIndex specifies which tab is currently selected.
// Gap is the string used to separate tabs (default " | ").
func TabBarStyle(tabs []TabData, activeIndex int, gap string) string {
	if len(tabs) == 0 {
		return ""
	}

	if gap == "" {
		gap = design.SeparatorStyle.Render(" | ")
	}

	var rendered []string
	for i, tab := range tabs {
		var styled string
		if i == activeIndex {
			styled = design.ActiveTabStyle.Render(tab.Label)
		} else {
			styled = design.InactiveTabStyle.Render(tab.Label)
		}
		rendered = append(rendered, styled)
	}

	return strings.Join(rendered, gap)
}

// TabBarWithSeparator renders a tab bar with an underline separator.
func TabBarWithSeparator(tabs []TabData, activeIndex int) string {
	if len(tabs) == 0 {
		return ""
	}

	var rendered []string
	totalWidth := 0

	for i, tab := range tabs {
		var styled string
		if i == activeIndex {
			styled = design.ActiveTabStyle.Render(tab.Label)
		} else {
			styled = design.InactiveTabStyle.Render(tab.Label)
		}
		rendered = append(rendered, styled)
		totalWidth += lipgloss.Width(styled)
	}

	// Add gap spacing
	gapWidth := 2
	totalWidth += gapWidth * (len(tabs) - 1)

	topRow := strings.Join(rendered, strings.Repeat(" ", gapWidth))

	// Draw indicator line under active tab
	if activeIndex >= 0 && activeIndex < len(tabs) {
		var offset int
		for i := 0; i < activeIndex; i++ {
			offset += lipgloss.Width(rendered[i]) + gapWidth
		}
		activeWidth := lipgloss.Width(rendered[activeIndex])

		line := strings.Repeat(" ", offset) +
			strings.Repeat("─", activeWidth) +
			strings.Repeat(" ", totalWidth-offset-activeWidth)

		return topRow + "\n" + design.AccentStyle.Render(line)
	}

	return topRow
}

// TabInline renders a single tab label (useful for custom layouts).
func TabInline(label string, isActive bool) string {
	if isActive {
		return design.ActiveTabStyle.Render(label)
	}
	return design.InactiveTabStyle.Render(label)
}

// TabCloseable renders a tab with a close indicator (e.g., "[x] Tab").
func TabCloseable(label string, isActive bool, showClose bool) string {
	var closeMark string
	if showClose {
		closeMark = design.MutedStyle.Render("[×]")
	} else {
		closeMark = design.MutedStyle.Render("[ ]")
	}

	labelStyle := design.InactiveTabStyle
	if isActive {
		labelStyle = design.ActiveTabStyle
	}

	return closeMark + " " + labelStyle.Render(label)
}

// TabBarVertical renders tabs in a vertical sidebar layout.
func TabBarVertical(tabs []TabData, activeIndex int, width int) string {
	if len(tabs) == 0 {
		return ""
	}

	var rendered []string
	for i, tab := range tabs {
		var styled string
		if i == activeIndex {
			styled = lipgloss.NewStyle().
				Width(width).
				Foreground(design.Accent).
				Background(design.SelBg).
				Padding(0, 1).
				Render(tab.Label)
		} else {
			styled = lipgloss.NewStyle().
				Width(width).
				Foreground(design.Subtext).
				Padding(0, 1).
				Render(tab.Label)
		}
		rendered = append(rendered, styled)
	}

	return strings.Join(rendered, "")
}
