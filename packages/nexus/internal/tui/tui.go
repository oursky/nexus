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
// In split mode (>=90 cols), shows workspace list left and detail/PTY right.
func (r *renderer) Render(m *model.AppModel) string {
	header := r.renderHeader(m)
	footer := r.renderFooter(m)

	// Not connected: no-profile or onramp IS the full screen
	if !m.Connected() {
		if m.ShowNoProfile() {
			return r.noProfile.View(m)
		}
		return r.onramp.View(m)
	}

	// Build the body based on terminal width
	w := m.Width()
	isThreePane := w >= 110
	isTwoPane := w >= 90

	var body string
	if isTwoPane || isThreePane {
		body = r.renderSplitBody(m, isTwoPane, isThreePane)
	} else {
		body = r.renderSingleBody(m)
	}

	// Assemble full screen
	fullWidth := lipgloss.NewStyle().Width(w)
	tabBar := r.renderTabBar(m)
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

	// Profile/onramp overlay when connected
	if m.CurrentView() == model.ViewOnramp {
		return r.onramp.View(m)
	}

	return base
}

// renderSingleBody renders content for terminals <90 cols (no split).
func (r *renderer) renderSingleBody(m *model.AppModel) string {
	switch m.CurrentView() {
	case model.ViewDetail:
		return r.renderDetail(m)
	case model.ViewSpotlight:
		return r.spotlight.View(m)
	case model.ViewSync:
		return r.sync.View(m)
	case model.ViewCreate:
		return r.create.View(m)
	case model.ViewHelp:
		return r.help.View(m)
	case model.ViewConnect:
		return r.connect.View(m)
	case model.ViewDashboard:
		return r.dashboard.View(m)
	default:
		return r.dashboard.View(m)
	}
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
	hints := []string{"↑/k up", "↓/j down", "/ filter", "Tab focus", "C connect", "q quit", "? more"}
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

// renderSplitBody composites left/right (and right sidebar for three-pane) panels.
func (r *renderer) renderSplitBody(m *model.AppModel, isTwoPane, isThreePane bool) string {
	colors := design.ActiveTheme.Colors
	leftW, centerW, rightW := paneDimensions(m.Width(), isThreePane, isTwoPane)
	bodyH := max(m.Height()-7, 4)

	// === LEFT PANE: workspace list ===
	leftInnerW := max(leftW-2, 4)
	leftInnerH := max(bodyH-2, 2)
	leftBorderColor := colors.Border
	if m.LeftFocused() {
		leftBorderColor = colors.Accent
	}

	listModel := m.WorkspaceList()
	listModel.SetSize(leftInnerW, leftInnerH)
	leftContent := listModel.View()

	leftPane := lipgloss.NewStyle().
		Width(leftInnerW).
		Height(leftInnerH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(leftBorderColor).
		Render(leftContent)

	// === CENTER PANE ===
	var centerContent string
	if m.PTYPane() != nil && m.ActiveTab() >= 0 {
		centerContent = m.PTYPane().Render()
	} else {
		centerContent = r.renderCenterContent(m, centerW, bodyH)
	}

	sepStyle := lipgloss.NewStyle().Foreground(colors.Border)

	if !isThreePane {
		centerBorderColor := colors.Border
		if m.PTYFocused() {
			centerBorderColor = colors.Accent
		}
		centerInnerW := max(centerW-1, 4)
		centerPane := lipgloss.NewStyle().
			Width(centerInnerW).
			Height(bodyH).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(centerBorderColor).
			Render(centerContent)
		return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, centerPane)
	}

	// Three-pane
	centerPane := lipgloss.NewStyle().
		Width(centerW).
		Height(bodyH).
		Render(centerContent)

	rightInnerW := max(rightW-2, 4)
	rightInnerH := max(bodyH-2, 2)
	rightBorderColor := colors.Border
	if m.RightFocused() {
		rightBorderColor = colors.Accent
	}
	rightContent := r.renderSidebar(m, rightInnerW, rightInnerH)
	rightPane := lipgloss.NewStyle().
		Width(rightInnerW).
		Height(rightInnerH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rightBorderColor).
		Render(rightContent)

	sep := sepStyle.Render("\u2502")
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, centerPane, sep, rightPane)
}

// renderCenterContent returns content for the center pane in split mode.
func (r *renderer) renderCenterContent(m *model.AppModel, width, height int) string {
	colors := design.ActiveTheme.Colors

	switch m.CurrentView() {
	case model.ViewDetail:
		return r.renderDetail(m)
	case model.ViewConnect:
		return r.connect.View(m)
	case model.ViewHelp:
		return r.help.View(m)
	default:
		hint := lipgloss.NewStyle().
			Foreground(colors.TextMuted).
			Render("select a workspace")
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, hint)
	}
}

// renderSidebar returns content for the right pane in three-pane mode.
func (r *renderer) renderSidebar(m *model.AppModel, width, height int) string {
	colors := design.ActiveTheme.Colors

	switch m.CurrentView() {
	case model.ViewSpotlight:
		return r.spotlight.View(m)
	case model.ViewSync:
		return r.sync.View(m)
	default:
		hint := lipgloss.NewStyle().
			Foreground(colors.TextMuted).
			Render("l:spotlight  y:sync")
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, hint)
	}
}

// paneDimensions returns (leftW, centerW, rightW) for the current layout.
func paneDimensions(width int, isThreePane, isTwoPane bool) (leftW, centerW, rightW int) {
	inner := max(width-4, 24)
	if isThreePane {
		leftW = int(float64(inner) * 0.28)
		rightW = int(float64(inner) * 0.22)
		centerW = max(inner-leftW-rightW-1, 20)
		return
	}
	if isTwoPane {
		leftW = int(float64(inner) * 0.28)
		centerW = max(inner-leftW, 20)
		rightW = 0
		return
	}
	leftW = inner
	centerW = inner
	rightW = 0
	return
}

// Run starts the TUI application.
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
