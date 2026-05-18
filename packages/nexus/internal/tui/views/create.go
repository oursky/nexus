package views

import (
	"fmt"
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
	form := m.CreateForm()
	w := m.Width()

	title := design.Title.Render("✨ Create Workspace")

	// Field rendering
	labelStyle := lipgloss.NewStyle().
		Foreground(colors.TextMuted).
		Width(12)
	activeStyle := lipgloss.NewStyle().
		Foreground(colors.Accent).
		Bold(true)
	errorStyle := lipgloss.NewStyle().
		Foreground(colors.Error).
		Bold(true)
	hintStyle := lipgloss.NewStyle().
		Foreground(colors.TextMuted).
		Italic(true)

	fields := []struct {
		label   string
		value   string
		focus   int
		placeholder string
	}{
		{"Name:", form.Name, 0, "my-workspace"},
		{"Repo:", form.Repo, 1, "https://github.com/user/repo"},
		{"Ref:", form.Ref, 2, "main"},
	}

	var lines []string
	for _, f := range fields {
		display := f.value
		if display == "" {
			display = hintStyle.Render(f.placeholder)
		}

		cursor := "  "
		if form.Focus == f.focus {
			cursor = activeStyle.Render("❯ ")
		}

		label := labelStyle.Render(f.label)
		if form.Focus == f.focus {
			label = activeStyle.Render(f.label)
		}

		lines = append(lines, fmt.Sprintf("%s%s %s", cursor, label, display))
	}

	// Error message
	if form.Err != "" {
		lines = append(lines, "")
		lines = append(lines, errorStyle.Render("  ⚠ "+form.Err))
	}

	// Help
	lines = append(lines, "")
	lines = append(lines, hintStyle.Render("  Tab next • Shift+Tab prev • Enter create • Esc cancel"))

	body := lipgloss.NewStyle().
		Padding(design.SpaceMD, design.SpaceLG).
		Width(w).
		Height(m.Height()-4).
		Render(strings.Join(lines, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}
