package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
	"github.com/oursky/nexus/packages/nexus/internal/tui/update"
	"github.com/oursky/nexus/packages/nexus/internal/tui/views"
)

// renderer implements model.ViewRenderer using the views package.
type renderer struct {
	dashboard *views.DashboardView
	connected bool
}

// Render dispatches to the appropriate view based on current state.
func (r *renderer) Render(m *model.AppModel) string {
	header := r.renderHeader(m)
	footer := r.renderFooter(m)

	var body string
	switch m.CurrentView() {
	case model.ViewDashboard:
		body = r.dashboard.View(m)
	default:
		body = r.dashboard.View(m)
	}

	// Stack: header + body + footer
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderHeader renders the top status bar with connection status.
func (r *renderer) renderHeader(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	statusDot := "●"
	statusText := "connected"
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, false, true, false).
		BorderForeground(colors.Border).
		Foreground(colors.Text).
		Bold(true)

	dotStyle := lipgloss.NewStyle().Foreground(colors.Success)
	textStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)

	content := "nexus " + dotStyle.Render(statusDot) + " " + textStyle.Render(statusText)
	return style.Render(content)
}

// renderFooter renders the keybinding hints bar.
func (r *renderer) renderFooter(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	hints := []string{"↑/k up", "↓/j down", "/ filter", "q quit", "? more"}
	hintStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)
	sepStyle := lipgloss.NewStyle().Foreground(colors.Border)

	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = hintStyle.Render(h)
	}
	footer := strings.Join(parts, sepStyle.Render(" • "))

	barStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), true, false, false, false).
		BorderForeground(colors.Border)

	return barStyle.Render(footer)
}

// daemonStatus returns a human-readable daemon connection status.
func daemonStatus(m *model.AppModel) string {
	if m.Mux() != nil {
		return "connected"
	}
	return "disconnected"
}

// Run starts the TUI application.
func Run(mux *rpc.MuxConn) error {
	appModel := model.NewAppModel(mux)

	// Create views
	dash := views.NewDashboardView(appModel.Width(), appModel.Height())

	// Wire the view renderer
	appModel.SetRenderer(&renderer{dashboard: dash})

	// Wire the update router
	appModel.SetUpdateRouter(func(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
		return update.Router(m, msg)
	})

	// Create and start the Bubble Tea program
	p := tea.NewProgram(
		appModel,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err := p.Run()
	return err
}
