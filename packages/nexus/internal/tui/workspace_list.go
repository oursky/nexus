package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// workspaceItem adapts workspace.Workspace for bubbles/list.
type workspaceItem struct {
	w          workspace.Workspace
	tree       string // "├ " or "└ "
	groupLabel string // non-empty for the first item in a project group
}

func (w workspaceItem) FilterValue() string {
	return w.w.WorkspaceName + " " + w.w.ID + " " + string(w.w.State)
}

func (w workspaceItem) Title() string {
	name := w.w.WorkspaceName
	if name == "" {
		name = "(unnamed)"
	}
	// Truncate at 40 runes so long names get "…" rather than a hard clip from
	// the delegate's MaxWidth. The delegate still constrains the rendered width.
	return fmt.Sprintf("%s%s  %s", w.tree, truncate(name, 40), statePill(w.w.State))
}

func (w workspaceItem) Description() string { return "" }

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

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// groupedDelegate renders workspaceItem rows with an optional project-group
// label on the first line (height=2 per item so the label has its own line).
type groupedDelegate struct {
	normalTitle   lipgloss.Style
	selectedTitle lipgloss.Style
	groupLabel    lipgloss.Style
}

func newGroupedDelegate() groupedDelegate {
	return groupedDelegate{
		normalTitle: lipgloss.NewStyle().Foreground(colorText),
		selectedTitle: lipgloss.NewStyle().
			Foreground(colorText).
			Background(colorSelBg).
			BorderLeft(true).
			BorderStyle(lipgloss.Border{Left: "▌"}).
			BorderForeground(colorAccent).
			PaddingLeft(1),
		groupLabel: lipgloss.NewStyle().Foreground(colorMuted),
	}
}

func (d groupedDelegate) Height() int  { return 2 }
func (d groupedDelegate) Spacing() int { return 0 }

func (d groupedDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d groupedDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	wi, ok := item.(workspaceItem)
	if !ok {
		return
	}
	width := m.Width()

	// Line 1: project group label (only for the first item in a group).
	var line1 string
	if wi.groupLabel != "" {
		line1 = d.groupLabel.MaxWidth(width).Render(wi.groupLabel)
	}

	// Line 2: workspace name + status pill.
	title := wi.Title()
	var line2 string
	if index == m.Index() {
		line2 = d.selectedTitle.MaxWidth(width - 2).Render(title)
	} else {
		line2 = d.normalTitle.MaxWidth(width).Render("  " + title)
	}

	if line1 != "" {
		fmt.Fprintf(w, "%s\n%s", line1, line2)
	} else {
		fmt.Fprintf(w, "\n%s", line2) // blank first line to keep height=2
	}
}

func newWorkspaceList(width, height int) list.Model {
	l := list.New([]list.Item{}, newGroupedDelegate(), width, height)
	l.Title = "Workspaces"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.Styles.Title = titleStyle
	return l
}

// workspacesToItems groups workspaces by project and returns a flat []list.Item
// of workspaceItem only (no header items). The first workspace in each group
// carries a groupLabel; all others leave it empty.
//
// Projects appear in first-seen API order so that the visual index of each
// workspace matches its position in the workspace.list API response. This is
// required for e2e tests that navigate by API index. Within each project group
// the original API order is preserved.
//
// projectsByID maps project ID → project name; pass nil for a flat list.
func workspacesToItems(workspaces []workspace.Workspace, projectsByID map[string]string) []list.Item {
	type group struct {
		name string
		ws   []workspace.Workspace
	}

	// First pass: collect groups in the order the first workspace of each
	// project appears in the API response.
	seen := map[string]int{} // project key → group slice index
	var groups []group

	for _, w := range workspaces {
		key := ""
		if w.ProjectID != "" {
			if name, ok := projectsByID[w.ProjectID]; ok && name != "" {
				key = name
			} else {
				key = w.ProjectID
			}
		}
		if idx, ok := seen[key]; ok {
			groups[idx].ws = append(groups[idx].ws, w)
		} else {
			seen[key] = len(groups)
			groups = append(groups, group{name: key, ws: []workspace.Workspace{w}})
		}
	}

	showLabels := len(groups) > 1 || (len(groups) == 1 && groups[0].name != "")

	var items []list.Item
	for _, g := range groups {
		for i, w := range g.ws {
			tree := "├ "
			if i == len(g.ws)-1 {
				tree = "└ "
			}
			if !showLabels {
				tree = ""
			}

			label := ""
			if showLabels && i == 0 {
				label = g.name
				if label == "" {
					label = "(no project)"
				}
			}
			items = append(items, workspaceItem{w: w, tree: tree, groupLabel: label})
		}
	}
	return items
}
