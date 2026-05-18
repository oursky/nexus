package commands

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/sshtunnel"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// IsSidebarTunnelLocal returns true when the active profile is a local daemon
// (no SSH tunnel needed for spotlight port forwarding).
func IsSidebarTunnelLocal(p *profile.Profile) bool {
	if p == nil {
		return true
	}
	if p.LocalPort > 0 {
		return true
	}
	h := strings.ToLower(strings.TrimSpace(p.Host))
	for _, local := range []string{"", "localhost", "127.0.0.1", "::1", "0.0.0.0"} {
		if h == local {
			return true
		}
	}
	return false
}

// StartSidebarTunnelCmd starts port forwarding for all active spotlight
// forwards for the given workspace. The result is delivered as tunnelStartedMsg.
//
// For remote daemons: spawns a single SSH process with multiple -L specs.
// For local daemons:
//   - If LocalPort == RemotePort for all forwards (process-isolation workspaces),
//     ports are directly accessible — no proxy needed.
//   - If LocalPort != RemotePort (libkrun VM workspaces where the daemon binds
//     an ephemeral vsock proxy port), creates a LocalProxy that binds each
//     LocalPort and forwards to the daemon's RemotePort on localhost.
func StartSidebarTunnelCmd(wsID string, fwds []*spotlight.Forward) tea.Cmd {
	return func() tea.Msg {
		p, err := profile.LoadDefault()
		if err != nil {
			return messages.TunnelStarted{WsID: wsID, Err: fmt.Errorf("load profile: %w", err)}
		}
		if IsSidebarTunnelLocal(p) {
			// Local daemon — check whether port remapping is needed (libkrun VM case).
			// For libkrun workspaces the daemon binds an ephemeral host-side TCP port
			// (Forward.RemotePort) that proxies via vsock to the VM's service port
			// (Forward.LocalPort). We need a local TCP proxy to map LocalPort → RemotePort.
			var proxyFwds []sshtunnel.Forward
			for _, f := range fwds {
				if f == nil || f.State != spotlight.ForwardStateActive {
					continue
				}
				if f.LocalPort == f.RemotePort {
					// Service is directly on the daemon host at the expected port.
					continue
				}
				rh := "127.0.0.1"
				if f.TargetHost != "" {
					rh = f.TargetHost
				}
				proxyFwds = append(proxyFwds, sshtunnel.Forward{
					LocalPort:  f.LocalPort,
					RemoteHost: rh,
					RemotePort: f.RemotePort,
				})
			}
			if len(proxyFwds) == 0 {
				// All ports directly accessible (process-isolation workspace on daemon host).
				return messages.TunnelStarted{WsID: wsID, Local: true}
			}
			// VM workspace: create local TCP proxy for port remapping.
			lp, err := sshtunnel.StartLocalProxy(proxyFwds)
			if err != nil {
				return messages.TunnelStarted{WsID: wsID, Err: fmt.Errorf("local proxy: %w", err)}
			}
			return messages.TunnelStarted{WsID: wsID, Local: true, LocalProxy: lp}
		}

		// Remote daemon — build SSH multi-tunnel specs.
		var specs []sshtunnel.Forward
		for _, f := range fwds {
			if f == nil || f.State != spotlight.ForwardStateActive {
				continue
			}
			rh := "127.0.0.1"
			if f.TargetHost != "" {
				rh = f.TargetHost
			}
			specs = append(specs, sshtunnel.Forward{
				LocalPort:  f.LocalPort,
				RemoteHost: rh,
				RemotePort: f.RemotePort,
			})
		}
		if len(specs) == 0 {
			return messages.TunnelStarted{WsID: wsID}
		}

		mt := sshtunnel.NewMultiWithOptions(p.Host, p.SSHPort, p.SSHIdentityFile, false)
		boundPorts, err := mt.Start(specs, 5*time.Second)
		if err != nil {
			return messages.TunnelStarted{WsID: wsID, Err: fmt.Errorf("SSH tunnel: %w", err)}
		}
		return messages.TunnelStarted{WsID: wsID, Tunnel: mt, BoundPorts: boundPorts}
	}
}
