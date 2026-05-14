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
	name := w.w.WorkspaceName
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("%s  %s", truncate(name, 28), statePill(w.w.State))
}

func statePill(state workspace.State) string {
	switch state {
	case workspace.StateRunning:
		return statusOkStyle.Render("● RUNNING")
	case workspace.StateStarting:
		return warningStyle.Render("⟳ STARTING")
	case workspace.StateSnapshotting:
		return warningStyle.Render("⟳ SNAPSHOTTING")
	case workspace.StateRestored:
		return warningStyle.Render("⟳ RESTORED")
	case workspace.StateStopped:
		return mutedStyle.Render("○ STOPPED")
	case workspace.StatePaused:
		return mutedStyle.Render("⏸ PAUSED")
	case workspace.StateCreated:
		return mutedStyle.Render("○ CREATED")
	default:
		return mutedStyle.Render(string(state))
	}
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
	d.Styles.NormalTitle = lipgloss.NewStyle().Foreground(colorText)
	d.Styles.NormalDesc = mutedStyle
	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(colorText).
		Background(colorSelBg).
		BorderLeft(true).
		BorderStyle(lipgloss.Border{Left: "▌"}).
		BorderForeground(colorAccent).
		PaddingLeft(1)
	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colorSubtext).
		Background(colorSelBg).
		BorderLeft(true).
		BorderStyle(lipgloss.Border{Left: " "}).
		BorderForeground(colorAccent).
		PaddingLeft(1)
	return d
}
