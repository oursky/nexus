package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

type CreateView struct{}

func NewCreateView() *CreateView {
	return &CreateView{}
}

func (v *CreateView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	width := m.Width()

	title := design.Title.Render("✨ Create Workspace")

	labelStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)
	valueStyle := lipgloss.NewStyle().Foreground(colors.Text)
	hintStyle := lipgloss.NewStyle().Foreground(colors.TextMuted).Italic(true)

	content := []string{
		labelStyle.Render("Create a new workspace from a git repository."),
		"",
		hintStyle.Render("Workspace creation via TUI is coming soon."),
		hintStyle.Render("For now, use the CLI:"),
		"",
		valueStyle.Bold(true).Render("  nexus workspace create --name <name> --repo <url>"),
	}

	body := lipgloss.NewStyle().
		Padding(design.SpaceMD).
		Width(width).
		Height(m.Height() - 4).
		Render(strings.Join(content, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}
