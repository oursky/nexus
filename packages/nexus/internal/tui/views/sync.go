package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SyncRow represents a single sync session row in the sync panel.
type SyncRow struct {
	SessionID string
	Status    string
	Direction string
	LocalPath string
	WorkspaceID string
}

// SyncPanelConfig holds configuration for rendering the sync panel.
type SyncPanelConfig struct {
	Width  int
	Rows   []SyncRow
	Sel    int
}

// RenderSyncPanel renders the volume sync panel.
func RenderSyncPanel(cfg SyncPanelConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Volume sync"))
	if len(cfg.Rows) == 0 {
		b.WriteString(mutedStyle.Render("No sync sessions. Press n to start (local path + direction).\n"))
	} else {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render("j/k select · n new · x stop · p pause · r resume"))
		for i, r := range cfg.Rows {
			line := fmt.Sprintf("%s  %s  %-12s  %s → %s", r.SessionID, r.Status, r.Direction, r.LocalPath, r.WorkspaceID)
			if i == cfg.Sel {
				fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render("› "+truncate(line, 72)))
			} else {
				fmt.Fprintf(&b, "%s\n", truncate(line, 72))
			}
		}
	}
	return lipgloss.NewStyle().MaxWidth(cfg.Width).Render(b.String())
}
