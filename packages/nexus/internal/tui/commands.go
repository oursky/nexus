package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/tui/commands"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

func openDaemonConn() tea.Cmd {
	return func() tea.Msg {
		mux, err := rpc.EnsureMux()
		if err != nil {
			return messages.ConnErr{Err: err}
		}
		return messages.ConnReady{Mux: mux}
	}
}

func (m Model) refreshCmd() tea.Cmd {
	mux := m.mux
	if mux == nil {
		return nil
	}
	return func() tea.Msg {
		// Fetch workspace list and project list in parallel.
		type wsResult struct {
			ws  []workspace.Workspace
			err error
		}
		type projResult struct {
			byID map[string]string
		}
		wsCh := make(chan wsResult, 1)
		projCh := make(chan projResult, 1)
		go func() {
			ws, err := commands.FetchWorkspaces(mux)
			wsCh <- wsResult{ws, err}
		}()
		go func() {
			projCh <- projResult{commands.FetchProjectsByID(mux)}
		}()
		wr := <-wsCh
		if wr.err != nil {
			return messages.WorkspacesErr{Err: wr.err}
		}
		pr := <-projCh
		return messages.Workspaces{Workspaces: wr.ws, ProjectsByID: pr.byID}
	}
}

func (m Model) loadDetailCmd(id string) tea.Cmd {
	return commands.LoadDetailCmd(m.mux, id)
}

func (m Model) rpcVoidCmd(method string, id string) tea.Cmd {
	return commands.RpcVoidCmd(m.mux, method, id)
}

func (m Model) loadSpotlightsCmd(wsID string) tea.Cmd {
	return commands.LoadSpotlightsCmd(m.mux, wsID)
}

// closeSidebarTunnel shuts down any active spotlight SSH tunnel or local proxy
// and resets tunnel state on the model. Safe to call when nothing is running.
func (m *Model) closeSidebarTunnel() {
	if m.sidebarTunnel != nil {
		_ = m.sidebarTunnel.Close()
		m.sidebarTunnel = nil
	}
	if m.sidebarLocalProxy != nil {
		_ = m.sidebarLocalProxy.Close()
		m.sidebarLocalProxy = nil
	}
	m.sidebarTunnelLive = false
	m.sidebarTunnelWsID = ""
	m.sidebarTunnelLocal = false
}

// isSidebarTunnelLocal returns true when the active profile is a local daemon
// (no SSH tunnel needed for spotlight port forwarding).
func isSidebarTunnelLocal(p *profile.Profile) bool {
	return commands.IsSidebarTunnelLocal(p)
}

// startSidebarTunnelCmd starts port forwarding for all active spotlight
// forwards for the given workspace. The result is delivered as tunnelStartedMsg.
//
// For remote daemons: spawns a single SSH process with multiple -L specs.
// For local daemons:
//   - If LocalPort == RemotePort for all forwards (process-isolation workspaces),
//     ports are directly accessible — no proxy needed.
//   - If LocalPort != RemotePort (libkrun VM workspaces where the daemon binds
//     an ephemeral vsock proxy port), creates a LocalProxy that binds each
//     LocalPort and forwards to the daemon's RemotePort on localhost.
func startSidebarTunnelCmd(wsID string, fwds []*spotlight.Forward) tea.Cmd {
	return commands.StartSidebarTunnelCmd(wsID, fwds)
}

// loadSidebarSpotCmd fetches spotlight.list for the currently selected
// workspace and delivers a sidebarSpotMsg.
func (m Model) loadSidebarSpotCmd() tea.Cmd {
	ws := m.selectedWorkspace()
	if ws == nil || m.mux == nil {
		return nil
	}
	return commands.LoadSidebarSpotCmd(m.mux, ws.ID)
}

// sidebarSpotTickCmd schedules the next sidebar spotlight refresh (3s).
func sidebarSpotTickCmd() tea.Cmd {
	return tea.Every(3*time.Second, func(time.Time) tea.Msg {
		return messages.SidebarSpotTick{}
	})
}

// loadSidebarDiscoveredCmd calls workspace.discover-ports for the selected
// workspace and delivers a sidebarDiscoveredMsg.
func (m Model) loadSidebarDiscoveredCmd() tea.Cmd {
	ws := m.selectedWorkspace()
	if ws == nil || m.mux == nil {
		return nil
	}
	return commands.LoadSidebarDiscoveredCmd(m.mux, ws.ID)
}

// stopWorkspaceSpotCmd stops all spotlight forwards for a workspace.
func (m Model) stopWorkspaceSpotCmd(wsID string) tea.Cmd {
	return commands.StopWorkspaceSpotCmd(m.mux, wsID)
}

// startAllDiscoveredCmd discovers ports for a workspace and starts spotlight for each.
func (m Model) startAllDiscoveredCmd(wsID string) tea.Cmd {
	return commands.StartAllDiscoveredCmd(m.mux, wsID)
}

func (m Model) loadSyncListCmd(wsID string) tea.Cmd {
	return commands.LoadSyncListCmd(m.mux, wsID)
}

func (m Model) startSpotlightCmd(wsID string, remotePort int) tea.Cmd {
	return commands.StartSpotlightCmd(m.mux, wsID, remotePort)
}

func (m Model) removeForwardCmd(wsID, forwardID string) tea.Cmd {
	return commands.RemoveForwardCmd(m.mux, wsID, forwardID)
}

func (m Model) syncStartCmd(wsID, localPath, direction string) tea.Cmd {
	return commands.SyncStartCmd(m.mux, wsID, localPath, direction)
}

func (m Model) syncStopSessionCmd(sessionID string) tea.Cmd {
	return commands.SyncStopSessionCmd(m.mux, sessionID)
}

func (m Model) syncPauseCmd(sessionID string) tea.Cmd {
	return commands.SyncPauseCmd(m.mux, sessionID)
}

func (m Model) syncResumeCmd(sessionID string) tea.Cmd {
	return commands.SyncResumeCmd(m.mux, sessionID)
}

func (m Model) forkCmd(parentID, childName string) tea.Cmd {
	return commands.ForkCmd(m.mux, parentID, childName)
}

func (m Model) createWorkspaceCmd(spec workspace.CreateSpec) tea.Cmd {
	return commands.CreateWorkspaceCmd(m.mux, spec)
}

func (m Model) shellExecCmd(wsID string) tea.Cmd {
	return commands.ShellExecCmd(wsID)
}

func tickRefresh() tea.Cmd {
	return commands.TickRefresh()
}

// ptyMouseModeCmd returns the tea.Cmd to adjust mouse capture when ptyFocused
// transitions from oldFocused to newFocused.
//
// When the PTY pane gains focus Bubble Tea's all-motion mouse capture is
// disabled so the host terminal emulator can perform native text selection via
// click-drag. When focus leaves the PTY pane, all-motion capture is restored so
// TUI click navigation (pane switching, list scrolling) keeps working.
//
// Returns nil when the focus state has not changed (safe to batch unconditionally).
func ptyMouseModeCmd(oldFocused, newFocused bool) tea.Cmd {
	return commands.PtyMouseModeCmd(oldFocused, newFocused)
}
