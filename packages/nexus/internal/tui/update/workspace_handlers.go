package update

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

func handleWorkspaceListReceived(msg messages.WorkspaceListReceived, m *model.AppModel) (tea.Model, tea.Cmd) {
	ws := make([]model.Workspace, 0, len(msg.Workspaces))
	for _, item := range msg.Workspaces {
		ws = append(ws, model.Workspace{
			ID:      item.ID,
			Name:    item.Name,
			Repo:    item.Repo,
			State:   item.State,
			Project: item.Project,
		})
	}
	m.SetWorkspaces(ws)

	// Populate the bubbles list with items for rendering
	items := make([]list.Item, len(msg.Workspaces))
	for i, item := range msg.Workspaces {
		items[i] = list.Item(item)
	}
	l := m.WorkspaceList()
	l.SetItems(items)
	m.SetWorkspaceList(l)

	return m, nil
}

func handleWorkspaceSelected(msg messages.WorkspaceSelected, m *model.AppModel) (tea.Model, tea.Cmd) {
	m.SetSelectedWS(msg.ID)
	m.SetCurrentView(model.ViewDetail)
	return m, nil
}

func handleWorkspaceStateChanged(msg messages.WorkspaceStateChanged, m *model.AppModel) (tea.Model, tea.Cmd) {
	ws := m.Workspaces()
	for i, w := range ws {
		if w.ID == msg.ID {
			w.State = msg.State
			ws[i] = w
			break
		}
	}
	m.SetWorkspaces(ws)

	m.AddToast(model.Toast{
		Message: "Workspace " + msg.ID + " is now " + msg.State,
		Kind:    mapStateToKind(msg.State),
	})

	return m, nil
}

func handleWorkspaceDeleteConfirmed(msg messages.WorkspaceDeleteConfirmed, m *model.AppModel) (tea.Model, tea.Cmd) {
	ws := m.Workspaces()
	for i, w := range ws {
		if w.ID == msg.ID {
			ws = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	m.SetWorkspaces(ws)

	m.AddToast(model.Toast{
		Message: "Workspace deleted: " + msg.ID,
		Kind:    messages.ToastInfo,
	})

	m.SetCurrentView(model.ViewDashboard)

	return m, nil
}

func handleWorkspaceFilterChanged(msg messages.WorkspaceFilterChanged, m *model.AppModel) (tea.Model, tea.Cmd) {
	m.SetFilterText(msg.Text)
	return m, nil
}

func handleCreateWorkspaceRequested(msg messages.WorkspaceCreateRequested, m *model.AppModel) (tea.Model, tea.Cmd) {
	m.SetCurrentView(model.ViewOnramp)
	return m, nil
}

func mapStateToKind(state string) messages.ToastKind {
	switch state {
	case "running":
		return messages.ToastSuccess
	case "starting":
		return messages.ToastInfo
	case "stopped":
		return messages.ToastWarning
	case "error":
		return messages.ToastError
	default:
		return messages.ToastInfo
	}
}
