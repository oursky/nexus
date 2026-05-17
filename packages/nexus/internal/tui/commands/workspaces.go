package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// FetchWorkspaces retrieves the workspace list from the daemon.
func FetchWorkspaces(mux *rpc.MuxConn) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Workspaces []workspace.Workspace `json:"workspaces"`
		}
		if err := mux.Call("workspace.list", map[string]any{}, &result); err != nil {
			return messages.DaemonDisconnected{Error: err}
		}
		items := make([]messages.WorkspaceItem, len(result.Workspaces))
		for i, ws := range result.Workspaces {
			items[i] = messages.WorkspaceItem{
				ID:    ws.ID,
				Name:  ws.WorkspaceName,
				Repo:  ws.Repo,
				State: string(ws.State),
			}
		}
		return messages.WorkspaceListReceived{Workspaces: items}
	}
}

// StartWorkspace starts a workspace.
func StartWorkspace(mux *rpc.MuxConn, id string) tea.Cmd {
	return func() tea.Msg {
		if err := mux.Call("workspace.start", map[string]any{"id": id}, nil); err != nil {
			return messages.ToastShown{Message: "Start failed: " + err.Error(), Kind: messages.ToastError}
		}
		return messages.WorkspaceStateChanged{ID: id, State: "starting"}
	}
}

// StopWorkspace stops a workspace.
func StopWorkspace(mux *rpc.MuxConn, id string) tea.Cmd {
	return func() tea.Msg {
		if err := mux.Call("workspace.stop", map[string]any{"id": id}, nil); err != nil {
			return messages.ToastShown{Message: "Stop failed: " + err.Error(), Kind: messages.ToastError}
		}
		return messages.WorkspaceStateChanged{ID: id, State: "stopping"}
	}
}

// DeleteWorkspace deletes a workspace.
func DeleteWorkspace(mux *rpc.MuxConn, id string) tea.Cmd {
	return func() tea.Msg {
		if err := mux.Call("workspace.delete", map[string]any{"id": id}, nil); err != nil {
			return messages.ToastShown{Message: "Delete failed: " + err.Error(), Kind: messages.ToastError}
		}
		return messages.WorkspaceDeleteConfirmed{ID: id}
	}
}

// ForkWorkspace forks a workspace.
func ForkWorkspace(mux *rpc.MuxConn, id string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Workspace workspace.Workspace `json:"workspace"`
		}
		if err := mux.Call("workspace.fork", map[string]any{"id": id}, &result); err != nil {
			return messages.ToastShown{Message: "Fork failed: " + err.Error(), Kind: messages.ToastError}
		}
		return messages.ToastShown{
			Message: "Fork created: " + result.Workspace.WorkspaceName,
			Kind:    messages.ToastSuccess,
		}
	}
}
