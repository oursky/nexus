package commands

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// LoadSpotlightsCmd returns a command that fetches the list of spotlight
// forwards for a workspace and delivers a SpotlightsData message.
func LoadSpotlightsCmd(mux Mux, wsID string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.SpotlightsData{Err: fmt.Errorf("not connected")}
		}
		var result struct {
			Forwards []*spotlight.Forward `json:"forwards"`
		}
		if err := mux.Call("spotlight.list", map[string]any{"workspaceId": wsID}, &result); err != nil {
			return messages.SpotlightsData{Err: err}
		}
		return messages.SpotlightsData{Forwards: result.Forwards}
	}
}

// LoadSidebarSpotCmd fetches spotlight.list for the currently selected
// workspace and delivers a SidebarSpot message.
func LoadSidebarSpotCmd(mux Mux, wsID string) tea.Cmd {
	if mux == nil {
		return nil
	}
	return func() tea.Msg {
		var result struct {
			Forwards []*spotlight.Forward `json:"forwards"`
		}
		if err := mux.Call("spotlight.list", map[string]any{"workspaceId": wsID}, &result); err != nil {
			return messages.SidebarSpot{WsID: wsID, Err: err}
		}
		return messages.SidebarSpot{WsID: wsID, Forwards: result.Forwards}
	}
}

// SidebarSpotTickCmd schedules the next sidebar spotlight refresh (3s).
func SidebarSpotTickCmd() tea.Cmd {
	return tea.Every(3*time.Second, func(time.Time) tea.Msg {
		return messages.SidebarSpotTick{}
	})
}

// LoadSidebarDiscoveredCmd calls workspace.discover-ports for the selected
// workspace and delivers a SidebarDiscovered message.
func LoadSidebarDiscoveredCmd(mux Mux, wsID string) tea.Cmd {
	if mux == nil {
		return nil
	}
	return func() tea.Msg {
		var ports []workspace.DiscoveredPort
		if err := mux.Call("workspace.discover-ports", map[string]any{"id": wsID}, &ports); err != nil {
			return messages.SidebarDiscovered{WsID: wsID, Err: err}
		}
		return messages.SidebarDiscovered{WsID: wsID, Ports: ports}
	}
}

// StopWorkspaceSpotCmd stops all spotlight forwards for a workspace.
func StopWorkspaceSpotCmd(mux Mux, wsID string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("spotlight.stop", map[string]any{"workspaceId": wsID}, nil); err != nil {
			return messages.MutationDone{Err: err}
		}
		return messages.MutationDone{Err: nil}
	}
}

// StartAllDiscoveredCmd discovers ports for a workspace and starts spotlight for each.
func StartAllDiscoveredCmd(mux Mux, wsID string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		var ports []workspace.DiscoveredPort
		if err := mux.Call("workspace.discover-ports", map[string]any{"id": wsID}, &ports); err != nil {
			return messages.MutationDone{Err: fmt.Errorf("discover ports: %w", err)}
		}
		if len(ports) == 0 {
			return messages.MutationDone{Err: fmt.Errorf("no ports discovered in workspace")}
		}
		for _, p := range ports {
			lp := p.LocalPort
			if lp <= 0 {
				lp = p.RemotePort
			}
			spec := spotlight.ExposeSpec{
				WorkspaceID: wsID,
				LocalPort:   lp,
				RemotePort:  p.RemotePort,
				Source:      spotlight.ForwardSourceAuto,
			}
			if err := mux.Call("spotlight.start", map[string]any{"workspaceId": wsID, "spec": spec}, nil); err != nil {
				return messages.MutationDone{Err: fmt.Errorf("start spotlight :%d: %w", p.RemotePort, err)}
			}
		}
		return messages.MutationDone{Err: nil}
	}
}

// StartSpotlightCmd returns a command that starts a spotlight forward
// for a given workspace and port.
func StartSpotlightCmd(mux Mux, wsID string, remotePort int) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		spec := spotlight.ExposeSpec{
			WorkspaceID: wsID,
			LocalPort:   remotePort,
			RemotePort:  remotePort,
			Source:      spotlight.ForwardSourceUser,
		}
		var result struct {
			Forward *spotlight.Forward `json:"forward"`
		}
		if err := mux.Call("spotlight.start", map[string]any{"workspaceId": wsID, "spec": spec}, &result); err != nil {
			return messages.MutationDone{Err: err}
		}
		return messages.MutationDone{Err: nil}
	}
}

// RemoveForwardCmd returns a command that removes a port forward rule.
func RemoveForwardCmd(mux Mux, wsID, forwardID string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.ports.remove", map[string]any{"workspaceId": wsID, "forwardId": forwardID}, nil); err != nil {
			return messages.MutationDone{Err: err}
		}
		return messages.MutationDone{Err: nil}
	}
}
