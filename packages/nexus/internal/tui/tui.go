package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/tui/components"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
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
	case model.ViewDetail:
		body = r.renderDetail(m)
	case model.ViewDashboard:
		body = r.dashboard.View(m)
	default:
		body = r.dashboard.View(m)
	}

	// Stack: header + body + footer
	base := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)

	// Overlay toasts on top of the base content
	toasts := r.renderToasts(m)
	if toasts != "" {
		// Place toasts at the bottom-right of the screen
		base = lipgloss.JoinVertical(lipgloss.Left, base, toasts)
	}

	return base
}

// renderToasts renders active toast notifications.
func (r *renderer) renderToasts(m *model.AppModel) string {
	modelToasts := m.Toasts()
	if len(modelToasts) == 0 {
		return ""
	}

	colors := design.ActiveTheme.Colors
	var rendered []string
	for _, t := range modelToasts {
		// Convert model.ToastKind to components.ToastLevel
		var level components.ToastLevel
		switch t.Kind {
		case messages.ToastSuccess:
			level = components.ToastSuccess
		case messages.ToastError:
			level = components.ToastError
		case messages.ToastWarning:
			level = components.ToastWarning
		default:
			level = components.ToastInfo
		}

		ct := &components.Toast{
			Level:   level,
			Message: t.Message,
			Time:    time.Now(),
		}
		rendered = append(rendered, ct.View())
	}

	// Stack toasts vertically, right-aligned
	toastBox := lipgloss.NewStyle().
		Align(lipgloss.Right).
		Foreground(colors.Text).
		MaxWidth(m.Width())

	return toastBox.Render(strings.Join(rendered, "\n"))
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

// renderDetail renders the workspace detail view.
func (r *renderer) renderDetail(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors

	// Find selected workspace
	var ws *model.Workspace
	for i, w := range m.Workspaces() {
		if w.ID == m.SelectedWS() {
			ws = &m.Workspaces()[i]
			break
		}
	}

	if ws == nil {
		return lipgloss.NewStyle().
			Foreground(colors.TextMuted).
			Render("No workspace selected")
	}

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(colors.Text).
		MarginBottom(design.SpaceSM)
	title := titleStyle.Render(ws.Name)

	// Info rows
	infoStyle := lipgloss.NewStyle().Foreground(colors.Text)
	labelStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)
	sepStyle := lipgloss.NewStyle().Foreground(colors.Border)

	info := []string{
		labelStyle.Render("ID:    ") + infoStyle.Render(ws.ID),
		labelStyle.Render("Repo:  ") + infoStyle.Render(ws.Repo),
		labelStyle.Render("State: ") + infoStyle.Render(ws.State),
		labelStyle.Render("Path:  ") + infoStyle.Render(ws.Path),
	}
	infoBlock := strings.Join(info, "\n")

	// Key hints
	hints := []string{"s start", "x stop", "d delete", "f fork", "t terminal", "l spotlight", "y sync", "Esc back"}
	hintStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = hintStyle.Render(h)
	}
	hintsBar := strings.Join(parts, sepStyle.Render(" • "))

	// Combine
	content := lipgloss.JoinVertical(lipgloss.Left, title, infoBlock, "", hintsBar)
	return lipgloss.NewStyle().
		Padding(design.SpaceMD).
		Width(m.Width()).
		Height(m.Height() - 4).
		Render(content)
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
