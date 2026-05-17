package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
	"github.com/oursky/nexus/packages/nexus/internal/tui/update"
	"github.com/oursky/nexus/packages/nexus/internal/tui/views"
)

// renderer implements model.ViewRenderer using the views package.
type renderer struct {
	dashboard *views.DashboardView
}

// Render dispatches to the appropriate view based on current state.
func (r *renderer) Render(m *model.AppModel) string {
	switch m.CurrentView() {
	case model.ViewDashboard:
		return r.dashboard.View(m)
	default:
		return r.dashboard.View(m)
	}
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
