package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// workspaceItem adapts workspace.Workspace for bubbles/list.
type workspaceItem struct {
	w workspace.Workspace
}

func (w workspaceItem) FilterValue() string {
	return w.w.WorkspaceName + " " + w.w.ID + " " + string(w.w.State)
}

func (w workspaceItem) Title() string {
	st := string(w.w.State)
	name := w.w.WorkspaceName
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("%s  •  %s", truncate(name, 28), st)
}

func (w workspaceItem) Description() string {
	return truncate(w.w.ID, 68)
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

func newWorkspaceList(delegate list.ItemDelegate, width, height int) list.Model {
	l := list.New([]list.Item{}, delegate, width, height)
	l.Title = "Workspaces"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.Styles.Title = titleStyle
	return l
}

func workspacesToItems(workspaces []workspace.Workspace) []list.Item {
	items := make([]list.Item, len(workspaces))
	for i := range workspaces {
		items[i] = workspaceItem{w: workspaces[i]}
	}
	return items
}

func styledDelegate() list.ItemDelegate {
	d := list.NewDefaultDelegate()
	d.Styles.NormalTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8E8E8"))
	d.Styles.NormalDesc = mutedStyle
	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFDF5")).
		Padding(0, 0, 0, 2)
	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#A8A8A8")).
		Padding(0, 0, 0, 2)
	return d
}
