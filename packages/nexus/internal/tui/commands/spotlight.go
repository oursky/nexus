package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// FetchForwards fetches the list of port forwards for a workspace.
func FetchForwards(mux *rpc.MuxConn, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Forwards []struct {
				ID          string `json:"id"`
				WorkspaceID string `json:"workspaceId"`
				LocalPort   int    `json:"localPort"`
				RemotePort  int    `json:"remotePort"`
				Label       string `json:"label"`
				Status      string `json:"status"`
			} `json:"forwards"`
		}
		err := mux.Call("workspace.ports.list", map[string]any{"workspaceId": workspaceID}, &result)
		if err != nil {
			return messages.DaemonDisconnected{Error: err}
		}
		items := make([]messages.PortForwardItem, len(result.Forwards))
		for i, f := range result.Forwards {
			items[i] = messages.PortForwardItem{
				ID:          f.ID,
				WorkspaceID: f.WorkspaceID,
				LocalPort:   f.LocalPort,
				RemotePort:  f.RemotePort,
				Label:       f.Label,
				Status:      f.Status,
			}
		}
		return messages.SpotlightRefreshed{Items: items}
	}
}

// StartForward starts a new port forward for a workspace.
func StartForward(mux *rpc.MuxConn, workspaceID string, localPort int, remotePort int, label string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Forward struct {
				ID          string `json:"id"`
				WorkspaceID string `json:"workspaceId"`
				LocalPort   int    `json:"localPort"`
				RemotePort  int    `json:"remotePort"`
				Label       string `json:"label"`
				Status      string `json:"status"`
			} `json:"forward"`
		}
		spec := map[string]any{
			"localPort":  localPort,
			"remotePort": remotePort,
			"label":      label,
		}
		err := mux.Call("workspace.ports.add", map[string]any{"workspaceId": workspaceID, "spec": spec}, &result)
		if err != nil {
			return messages.ToastShown{Message: "Failed to start forward: " + err.Error(), Kind: messages.ToastError}
		}
		return messages.PortForwardAdded{
			ForwardID:  result.Forward.ID,
			LocalPort:  result.Forward.LocalPort,
			RemotePort: result.Forward.RemotePort,
			Label:      result.Forward.Label,
		}
	}
}

// RemoveForward removes a port forward from a workspace.
func RemoveForward(mux *rpc.MuxConn, workspaceID string, forwardID string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Closed bool `json:"closed"`
		}
		err := mux.Call("workspace.ports.remove", map[string]any{"workspaceId": workspaceID, "forwardId": forwardID}, &result)
		if err != nil {
			return messages.ToastShown{Message: "Failed to remove forward: " + err.Error(), Kind: messages.ToastError}
		}
		return messages.PortForwardRemoved{ForwardID: forwardID}
	}
}

// StopSpotlight stops the spotlight for a workspace.
func StopSpotlight(mux *rpc.MuxConn, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Closed bool `json:"closed"`
		}
		err := mux.Call("spotlight.stop", map[string]any{"workspaceId": workspaceID}, &result)
		if err != nil {
			return messages.ToastShown{Message: "Failed to stop spotlight: " + err.Error(), Kind: messages.ToastError}
		}
		return messages.ToastShown{Message: "Spotlight stopped", Kind: messages.ToastSuccess}
	}
}
