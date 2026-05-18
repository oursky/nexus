package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

type ConnectView struct{}

func NewConnectView() *ConnectView {
	return &ConnectView{}
}

func (v *ConnectView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	width := m.Width()

	title := design.Title.Render("🔗 Connect to Workspace")

	var ws *model.Workspace
	for i, w := range m.Workspaces() {
		if w.ID == m.SelectedWS() {
			ws = &m.Workspaces()[i]
			break
		}
	}

	if ws == nil {
		body := lipgloss.NewStyle().Padding(design.SpaceMD).Width(width).Render("No workspace selected.")
		return lipgloss.JoinVertical(lipgloss.Left, title, body)
	}

	labelStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)
	valueStyle := lipgloss.NewStyle().Foreground(colors.Text)
	codeStyle := lipgloss.NewStyle().
		Foreground(colors.Text).
		Background(lipgloss.Color("#1a1b26")).
		Padding(0, 1)

	var content []string

	content = append(content, labelStyle.Render("Workspace: ")+valueStyle.Render(ws.Name))
	content = append(content, labelStyle.Render("ID:        ")+valueStyle.Render(ws.ID))
	content = append(content, "")

	if ws.State != "running" {
		content = append(content, lipgloss.NewStyle().
			Foreground(colors.Warning).
			Render("⚠ Workspace is not running. Start it first (s)."))
		content = append(content, "")
	}

	hostAlias := "nexus-vm-" + ws.ID
	content = append(content, labelStyle.Bold(true).Render("SSH:"))
	if ws.State == "running" {
		sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/dev/null root@%s", hostAlias)
		content = append(content, codeStyle.Render("  "+sshCmd))
	} else {
		content = append(content, valueStyle.Render("  (start workspace first)"))
	}
	content = append(content, "")

	content = append(content, labelStyle.Bold(true).Render("Editors:"))
	vsCodeCmd := fmt.Sprintf("code --remote ssh-remote+%s /workspace", hostAlias)
	cursorCmd := fmt.Sprintf("cursor --remote ssh-remote+%s /workspace", hostAlias)
	content = append(content, labelStyle.Render("  VS Code:  ")+codeStyle.Render(vsCodeCmd))
	content = append(content, labelStyle.Render("  Cursor:   ")+codeStyle.Render(cursorCmd))
	content = append(content, "")

	content = append(content, labelStyle.Bold(true).Render("CLI:"))
	content = append(content, valueStyle.Render("  nexus workspace shell "+ws.ID))
	content = append(content, valueStyle.Render("  nexus workspace open-editor "+ws.ID+" --app vscode"))
	content = append(content, "")

	content = append(content, lipgloss.NewStyle().Italic(true).Foreground(colors.TextMuted).Render(
		"Note: Ensure SSH config is set up via 'nexus workspace open-editor' or manual config."))

	content = append(content, "")
	content = append(content, labelStyle.Render("Press Esc to go back"))

	body := lipgloss.NewStyle().
		Padding(design.SpaceMD).
		Width(width).
		Height(m.Height() - 4).
		Render(strings.Join(content, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}
