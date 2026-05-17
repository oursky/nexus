package views

import (
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

	content := []string{
		labelStyle.Render("Workspace: ") + valueStyle.Render(wsName),
		"",
		labelStyle.Render("Port forwards will appear here when configured."),
		"",
		lipgloss.NewStyle().Foreground(colors.TextMuted).Italic(true).Render(
			"Use the CLI to manage port forwards: nexus port-forward <workspace>",
		),
	}

	body := lipgloss.NewStyle().
		Padding(design.SpaceMD).
		Width(width).
		Height(m.Height() - 4).
		Render(strings.Join(content, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}
