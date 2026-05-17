package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
)

// RefreshSpotlight refreshes the spotlight state.
func RefreshSpotlight(mux *rpc.MuxConn, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement RPC call to get forwards
		return messages.ConnectionStatusMsg{Status: "spotlight refreshed"}
	}
}

// AddPortForward adds a new port forward.
func AddPortForward(mux *rpc.MuxConn, workspaceID string, port int, label string) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement RPC call
		return messages.PortForwardAdded{Port: port, Label: label}
	}
}

// RemovePortForward removes a port forward.
func RemovePortForward(mux *rpc.MuxConn, workspaceID string, port int) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement RPC call
		return messages.PortForwardRemoved{Port: port}
	}
}

// ToggleAllForwards toggles all forwards on/off.
func ToggleAllForwards(mux *rpc.MuxConn, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement RPC call
		return messages.PortForwardToggled{Port: 0}
	}
}
