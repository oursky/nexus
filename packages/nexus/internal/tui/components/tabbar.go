package components

import (
	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

type Tab struct {
	ID    string
	Label string
	Close bool // show close button
}

type TabBar struct {
	Tabs   []Tab
	Active int
	Width  int
}

type TabSelectedMsg struct {
	ID string
}

type TabClosedMsg struct {
	ID string
}

func (t *TabBar) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyLeft:
			if t.Active > 0 {
				t.Active--
				return func() tea.Msg {
					return TabSelectedMsg{ID: t.Tabs[t.Active].ID}
				}
			}
		case tea.KeyRight:
			if t.Active < len(t.Tabs)-1 {
				t.Active++
				return func() tea.Msg {
					return TabSelectedMsg{ID: t.Tabs[t.Active].ID}
				}
			}
		}
	}
	return nil
}

func (t *TabBar) View() string {
	if len(t.Tabs) == 0 {
		return ""
	}

	c := design.ActiveTheme.Colors

	// Render tabs
	tabViews := make([]string, len(t.Tabs))
	for i, tab := range t.Tabs {
		var tabStyle lipgloss.Style
		if i == t.Active {
			tabStyle = lipgloss.NewStyle().
				Foreground(c.Text).
				Background(c.BgSelected).
				Padding(0, design.SpaceSM)
		} else {
			tabStyle = lipgloss.NewStyle().
				Foreground(c.TextSubtle).
				Padding(0, design.SpaceSM)
		}

		tabViews[i] = tabStyle.Render(tab.Label)
	}

	// Join tabs with a separator
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabViews...)

	// Add bottom border
	tabBar = lipgloss.NewStyle().
		Border(lipgloss.Border{Bottom: "─"}).
		BorderForeground(c.Border).
		BorderBottom(true).
		Width(t.Width).
		Render(tabBar)

	return tabBar
}
