package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

type SpotlightView struct{}

func NewSpotlightView() *SpotlightView {
	return &SpotlightView{}
}

func (v *SpotlightView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	width := m.Width()

	title := design.Title.Render("📡 Spotlight — Port Forwarding")

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

	var content []string
	content = append(content, labelStyle.Render("Workspace: ")+valueStyle.Render(wsName))
	content = append(content, "")

	forwards := m.Forwards()

	if len(forwards) == 0 {
		content = append(content, labelStyle.Render("No port forwards configured"))
	} else {
		header := fmt.Sprintf("%-12s %-15s %s", "Label", "Local→Remote", "Status")
		content = append(content, lipgloss.NewStyle().Bold(true).Foreground(colors.Text).Render(header))
		for _, f := range forwards {
			line := fmt.Sprintf("%-12s %d→%-11d %s", f.Label, f.LocalPort, f.RemotePort, f.Status)
			content = append(content, lipgloss.NewStyle().Foreground(colors.Text).Render(line))
		}
	}

	content = append(content, "")
	content = append(content, lipgloss.NewStyle().Foreground(colors.TextMuted).Render("a add • r remove • Esc back"))

	body := lipgloss.NewStyle().
		Padding(design.SpaceMD).
		Width(width).
		Height(m.Height() - 4).
		Render(strings.Join(content, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}
