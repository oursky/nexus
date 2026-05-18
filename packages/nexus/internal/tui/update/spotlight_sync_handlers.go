package update

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/commands"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

func handleSpotlightRefreshRequested(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, commands.FetchForwards(m.Mux(), m.SelectedWS())
}

func handlePortForwardAdded(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, commands.FetchForwards(m.Mux(), m.SelectedWS())
}

func handlePortForwardRemoved(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, commands.FetchForwards(m.Mux(), m.SelectedWS())
}

func handlePortForwardToggled(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, commands.FetchForwards(m.Mux(), m.SelectedWS())
}

func handleSyncStartRequested(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	wsID := m.SelectedWS()
	m.AddToast(model.Toast{
		Kind:    messages.ToastInfo,
		Message: "Starting sync...",
	})
	return m, tea.Batch(
		commands.StartSync(m.Mux(), wsID, ""),
		commands.FetchSyncSessions(m.Mux(), wsID),
	)
}

func handleSyncPauseRequested(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	sessions := m.SyncSessions()
	if len(sessions) > 0 {
		return m, commands.PauseSync(m.Mux(), sessions[0].ID)
	}
	return m, nil
}

func handleSyncResumeRequested(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	sessions := m.SyncSessions()
	if len(sessions) > 0 {
		return m, commands.ResumeSync(m.Mux(), sessions[0].ID)
	}
	return m, nil
}

func handleSyncStopRequested(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	wsID := m.SelectedWS()
	m.AddToast(model.Toast{
		Kind:    messages.ToastInfo,
		Message: "Stopping sync...",
	})
	return m, tea.Batch(
		commands.StopSync(m.Mux(), wsID),
		commands.FetchSyncSessions(m.Mux(), wsID),
	)
}

func handleSyncStatusReceivedHandler(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, commands.FetchSyncSessions(m.Mux(), m.SelectedWS())
}
