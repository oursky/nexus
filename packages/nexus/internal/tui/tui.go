package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/tui/commands"
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
	spotlight *views.SpotlightView
	sync      *views.SyncView
	create    *views.CreateView
	help      *views.HelpView
	connect   *views.ConnectView
	onramp    *views.OnrampView
	noProfile *views.NoProfileView
	connected bool
}

// Render dispatches to the appropriate view based on current state.
func (r *renderer) Render(m *model.AppModel) string {
	header := r.renderHeader(m)
	footer := r.renderFooter(m)

	// Show onramp wizard if not connected
	if !m.Connected() {
		var body string
		if m.ShowNoProfile() {
			body = r.noProfile.View(m)
		} else {
			body = r.onramp.View(m)
		}
		// Force each layer to full width for consistent layout
		fullWidth := lipgloss.NewStyle().Width(m.Width())
		return lipgloss.JoinVertical(lipgloss.Left,
			fullWidth.Render(header),
			fullWidth.Render(body),
			fullWidth.Render(footer),
		)
	}

	var body string
	if m.PTYPane() != nil && m.ActiveTab() >= 0 {
		body = m.PTYPane().Render()
	} else {
		switch m.CurrentView() {
		case model.ViewDetail:
			body = r.renderDetail(m)
		case model.ViewSpotlight:
			body = r.spotlight.View(m)
		case model.ViewSync:
			body = r.sync.View(m)
		case model.ViewCreate:
			body = r.create.View(m)
		case model.ViewHelp:
			body = r.help.View(m)
		case model.ViewConnect:
			body = r.connect.View(m)
		case model.ViewDashboard:
			body = r.dashboard.View(m)
		default:
			body = r.dashboard.View(m)
		}
	}

	tabBar := r.renderTabBar(m)
	// Force full width for each layout layer
	fullWidth := lipgloss.NewStyle().Width(m.Width())
	layers := []string{
		fullWidth.Render(header),
		fullWidth.Render(tabBar),
		fullWidth.Render(body),
		fullWidth.Render(footer),
	}
	base := lipgloss.JoinVertical(lipgloss.Left, layers...)

	toasts := r.renderToasts(m)
	if toasts != "" {
		base = lipgloss.JoinVertical(lipgloss.Left, base, toasts)
	}

	return base
}

func (r *renderer) renderTabBar(m *model.AppModel) string {
	tabs := m.Tabs()
	if len(tabs) == 0 {
		return ""
	}

	colors := design.ActiveTheme.Colors
	var tabStrs []string
	for i, t := range tabs {
		style := lipgloss.NewStyle().Padding(0, 2).Foreground(colors.TextMuted)
		if i == m.ActiveTab() {
			style = style.Foreground(colors.Text).Bold(true).Underline(true)
		}
		tabStrs = append(tabStrs, style.Render(t.Label))
	}

	tabBar := strings.Join(tabStrs, lipgloss.NewStyle().Foreground(colors.Border).Render("│"))
	return lipgloss.NewStyle().Padding(0, 1).Render(tabBar)
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

	var statusText string
	var dotColor lipgloss.TerminalColor
	if m.Connected() {
		statusText = "connected"
		dotColor = colors.Success
	} else {
		statusText = "disconnected"
		dotColor = colors.Error
	}

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, false, true, false).
		BorderForeground(colors.Border).
		Foreground(colors.Text).
		Bold(true).
		Width(m.Width()) // Fill full terminal width

	dotStyle := lipgloss.NewStyle().Foreground(dotColor)
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
		BorderForeground(colors.Border).
		Width(m.Width()) // Fill full terminal width

	return barStyle.Render(footer)
}

// Run starts the TUI application.
// It attempts to connect to the daemon via stored profile.
// If connection fails, the onramp/connect wizard is shown.
func Run() error {
	// Try connecting with existing profile
	var initialMux *rpc.MuxConn
	ws, err := rpc.EnsureDaemon()
	if err == nil {
		initialMux = rpc.NewMuxConn(ws)
	}

	appModel := model.NewAppModel(initialMux)

	dash := views.NewDashboardView(appModel.Width(), appModel.Height())
	spot := views.NewSpotlightView()
	syncV := views.NewSyncView()
	create := views.NewCreateView()
	help := views.NewHelpView()
	connect := views.NewConnectView()
	onramp := views.NewOnrampView(appModel.Width(), appModel.Height())
	noProfile := views.NewNoProfileView()

	r := &renderer{
		dashboard: dash,
		spotlight: spot,
		sync:      syncV,
		create:    create,
		help:      help,
		connect:   connect,
		onramp:    onramp,
		noProfile: noProfile,
	}
	appModel.SetRenderer(r)

	// Update router
	appModel.SetUpdateRouter(func(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
		return update.Router(m, msg)
	})

	// Wire polling command factory
	if initialMux != nil {
		appModel.SetPollCmd(func() tea.Cmd {
			return commands.PollWorkspacesCmd(initialMux)
		})
	}

	p := tea.NewProgram(
		appModel,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err = p.Run()
	return err
}
