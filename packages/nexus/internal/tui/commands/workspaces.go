package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// FetchWorkspaces retrieves the workspace list from the daemon.
func FetchWorkspaces(mux *rpc.MuxConn) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement actual RPC call
		// Example: list := rpc.WorkspaceList(mux)
		// For now, return empty list
		return messages.WorkspaceListReceived{
			Workspaces: []messages.WorkspaceItem{},
		}
	}
}

// StartWorkspace starts a workspace.
func StartWorkspace(mux *rpc.MuxConn, id string) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement RPC call
		return messages.WorkspaceStateChanged{ID: id, State: "starting"}
	}
}

// StopWorkspace stops a workspace.
func StopWorkspace(mux *rpc.MuxConn, id string) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement RPC call
		return messages.WorkspaceStateChanged{ID: id, State: "stopping"}
	}
}

// DeleteWorkspace deletes a workspace.
func DeleteWorkspace(mux *rpc.MuxConn, id string) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement RPC call
		return messages.WorkspaceDeleteConfirmed{ID: id}
	}
}

// ForkWorkspace forks a workspace.
func ForkWorkspace(mux *rpc.MuxConn, id string) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement RPC call
		return messages.ToastShown{
			Message: "Fork created: ws-new",
			Kind:    messages.ToastSuccess,
		}
	}
}
