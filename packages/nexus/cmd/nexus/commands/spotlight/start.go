package spotlight

import (
	"fmt"
	"io"
	"time"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/sshtunnel"
	"github.com/spf13/cobra"
)

// discoveredPort matches the server's DiscoveredPort DTO.
type discoveredPort struct {
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
	Service    string `json:"service,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Source     string `json:"source,omitempty"`
}

func startCommand() *cobra.Command {
	var workspaceID string

	cmd := &cobra.Command{
		Use:   "start <workspace-id>",
		Short: "Start spotlight — tunnels all discovered ports to localhost",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceID = args[0]
			conn, err := rpc.EnsureMux()
			if err != nil {
				return fmt.Errorf("nexus spotlight start: %w", err)
			}
			defer conn.Close()

			// Discover ports from docker-compose + workspace config.
			var ports []discoveredPort
			if err := conn.Call("workspace.discover-ports", map[string]any{"id": workspaceID}, &ports); err != nil {
				return fmt.Errorf("nexus spotlight start: port discovery failed: %w", err)
			}
			if len(ports) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no ports discovered")
				return nil
			}

			// Load profile for SSH tunneling.
			p, err := profile.LoadDefault()
			if err != nil {
				return fmt.Errorf("nexus spotlight start: %w", err)
			}

			// Stop any previously-active spotlight (daemon + tunnels).
			if err := stopClientActiveSpotlight(conn, p, cmd.OutOrStdout()); err != nil {
				return fmt.Errorf("nexus spotlight start: stop previous spotlight: %w", err)
			}

			// Create daemon-side spotlight forwards for each port.
			type forwardResult struct {
				port discoveredPort
				fwd  *spotlight.Forward
			}
			var results []forwardResult
			for _, port := range ports {
				spec := spotlight.ExposeSpec{
					WorkspaceID: workspaceID,
					LocalPort:   port.LocalPort,
					RemotePort:  port.RemotePort,
					Protocol:    port.Protocol,
				}
				var result struct {
					Forward *spotlight.Forward `json:"forward"`
				}
				if err := conn.Call("spotlight.start", map[string]any{
					"workspaceId": workspaceID,
					"spec":        spec,
				}, &result); err != nil {
					_ = conn.Call("spotlight.stop", map[string]any{"workspaceId": workspaceID}, nil)
					return fmt.Errorf("%d/%s: daemon forward failed: %w", port.LocalPort, port.Service, err)
				}
				results = append(results, forwardResult{port: port, fwd: result.Forward})
			}

			// Build a single MultiTunnel with all forwards in one SSH process.
			var fwds []sshtunnel.Forward
			for _, r := range results {
				rh := "127.0.0.1"
				rp := r.port.RemotePort
				if r.fwd != nil {
					if r.fwd.TargetHost != "" {
						rh = r.fwd.TargetHost
					}
					if r.fwd.RemotePort > 0 {
						rp = r.fwd.RemotePort
					}
				}
				fwds = append(fwds, sshtunnel.Forward{
					LocalPort:  r.port.LocalPort,
					RemoteHost: rh,
					RemotePort: rp,
				})
			}

			mt := sshtunnel.NewMultiWithOptions(p.Host, p.SSHPort, p.SSHIdentityFile, false)
			boundPorts, err := mt.Start(fwds, 5*time.Second)
			if err != nil {
				_ = conn.Call("spotlight.stop", map[string]any{"workspaceId": workspaceID}, nil)
				return fmt.Errorf("nexus spotlight start: SSH tunnel failed: %w", err)
			}

			if err := persistClientActiveSpotlight(p, workspaceID, mt.PID()); err != nil {
				_ = mt.Close()
				_ = conn.Call("spotlight.stop", map[string]any{"workspaceId": workspaceID}, nil)
				return fmt.Errorf("persist spotlight client state: %w", err)
			}

			for i, r := range results {
				label := r.port.Service
				if label == "" {
					label = fmt.Sprintf(":%d", r.port.RemotePort)
				}
				bp := boundPorts[i]
				if bp != r.port.LocalPort {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s → localhost:%d (remapped from :%d)\n", label, bp, r.port.LocalPort)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s → localhost:%d\n", label, bp)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "forwarded %d/%d ports\n", len(results), len(ports))
			return nil
		},
	}

	return cmd
}

func stopClientActiveSpotlight(conn *rpc.MuxConn, p *profile.Profile, out io.Writer) error {
	state, err := loadSpotlightClientState()
	if err != nil {
		return err
	}
	key := spotlightProfileKey(p)
	active, ok := state.Profiles[key]
	if !ok || active.WorkspaceID == "" {
		return nil
	}

	if err := conn.Call("spotlight.stop", map[string]any{"workspaceId": active.WorkspaceID}, nil); err != nil {
		fmt.Fprintf(out, "warning: failed to stop previous spotlight for %s: %v\n", active.WorkspaceID, err)
	}
	delete(state.Profiles, key)
	// Gracefully shut down all persisted SSH tunnel processes (handles old + new format).
	for _, pid := range active.allTunnelPIDs() {
		sshtunnel.CloseByPID(pid)
	}
	return saveSpotlightClientState(state)
}

func persistClientActiveSpotlight(p *profile.Profile, workspaceID string, pid int) error {
	state, err := loadSpotlightClientState()
	if err != nil {
		return err
	}
	key := spotlightProfileKey(p)
	if workspaceID == "" {
		delete(state.Profiles, key)
		return saveSpotlightClientState(state)
	}
	state.Profiles[key] = spotlightProfileState{WorkspaceID: workspaceID, TunnelPID: pid}
	return saveSpotlightClientState(state)
}
