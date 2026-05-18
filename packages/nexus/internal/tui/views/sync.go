package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

type SyncView struct{}

func NewSyncView() *SyncView {
	return &SyncView{}
}

func (v *SyncView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	width := m.Width()

	title := design.Title.Render("🔄 Sync")

	var wsName string
	for _, ws := range m.Workspaces() {
		if ws.ID == m.SelectedWS() {
			wsName = ws.Name
			break
		}
	}
	if wsName == "" {
		wsName = "unknown"
	}

	labelStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)
	valueStyle := lipgloss.NewStyle().Foreground(colors.Text)
	accentStyle := lipgloss.NewStyle().Foreground(colors.Accent).Bold(true)

	sessions := m.SyncSessions()

	var content []string
	content = append(content, labelStyle.Render("Workspace: ")+valueStyle.Render(wsName))
	content = append(content, "")

	if len(sessions) == 0 {
		content = append(content, labelStyle.Render("No active sync sessions."))
		content = append(content, "")
		content = append(content, lipgloss.NewStyle().Italic(true).Foreground(colors.TextMuted).Render(
			"Press 's' to start sync for this workspace"))
	} else {
		// Header row
		header := accentStyle.Render(fmt.Sprintf("  %-12s %-15s %-12s %s/%s",
			"Session", "Direction", "Status", "Sent", "Received"))
		content = append(content, header)

		// Separator
		sep := lipgloss.NewStyle().Foreground(colors.Border).Render(strings.Repeat("─", width-4))
		content = append(content, sep)

		// Session rows
		for _, s := range sessions {
			shortID := s.ID
			if len(shortID) > 10 {
				shortID = shortID[:10]
			}
			statusStyle := lipgloss.NewStyle().Foreground(colors.Text)
			if s.Status == "running" {
				statusStyle = lipgloss.NewStyle().Foreground(colors.Success)
			} else if s.Status == "paused" {
				statusStyle = lipgloss.NewStyle().Foreground(colors.Warning)
			}

			row := fmt.Sprintf("  %-12s %-15s ", shortID, s.Direction)
			row += statusStyle.Render(fmt.Sprintf("%-12s", s.Status))
			row += fmt.Sprintf(" %d/%d files", s.FilesSent, s.FilesReceived)
			content = append(content, valueStyle.Render(row))
		}

		content = append(content, "")
		content = append(content, labelStyle.Render(
			"s start • p pause • r resume • x stop • Esc back"))
	}

	body := lipgloss.NewStyle().
		Padding(design.SpaceMD).
		Width(width).
		Height(m.Height() - 4).
		Render(strings.Join(content, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}
