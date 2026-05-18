package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// DetailView renders a workspace's detailed information.
type DetailView struct {
	Workspace *workspace.Workspace
	Width     int
}

// Styles for detail view rendering.
var (
	detailKeyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	detailValStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229"))
)

// Render displays the workspace detail view.
func (v *DetailView) Render() string {
	ws := v.Workspace
	if ws == nil {
		return ""
	}
	keyw := 18
	width := v.Width
	row := func(k, val string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			detailKeyStyle.Width(keyw).Render(k),
			detailValStyle.Width(max(width-keyw-4, 20)).Render(val),
		)
	}
	ports := "(none)"
	if len(ws.TunnelPorts) > 0 {
		parts := make([]string, len(ws.TunnelPorts))
		for i, p := range ws.TunnelPorts {
			parts[i] = fmt.Sprintf("%d", p)
		}
		ports = strings.Join(parts, ", ")
	}
	b := strings.Builder{}
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render(ws.WorkspaceName))
	fmt.Fprintf(&b, "%s\n", row("ID", ws.ID))
	fmt.Fprintf(&b, "%s\n", row("State", string(ws.State)))
	fmt.Fprintf(&b, "%s\n", row("Backend", ws.Backend))
	fmt.Fprintf(&b, "%s\n", row("Guest IP", ws.GuestIP))
	fmt.Fprintf(&b, "%s\n", row("Repo", truncate(ws.Repo, 60)))
	fmt.Fprintf(&b, "%s\n", row("Ref", ws.Ref))
	fmt.Fprintf(&b, "%s\n", row("Root path", truncate(ws.RootPath, 60)))
	fmt.Fprintf(&b, "%s\n", row("Ports", ports))
	created := "—"
	if !ws.CreatedAt.IsZero() {
		created = ws.CreatedAt.UTC().Format(time.RFC3339)
	}
	updated := "—"
	if !ws.UpdatedAt.IsZero() {
		updated = ws.UpdatedAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "%s\n", row("Created", created))
	fmt.Fprintf(&b, "%s\n", row("Updated", updated))
	return b.String()
}

// RenderWorkspaceDetail renders a workspace detail view with the given width.
func RenderWorkspaceDetail(ws *workspace.Workspace, width int) string {
	v := &DetailView{Workspace: ws, Width: width}
	return v.Render()
}

// Helper functions.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
