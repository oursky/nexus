package spotlight

import (
	"fmt"
	"io"
	"sync"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/sshtunnel"
	"github.com/spf13/cobra"
)

// tunnelCache holds active SSH port forwards keyed by local port.
var tunnelCache struct {
	mu       sync.Mutex
	forwards map[int]*sshtunnel.Manager // localPort → tunnel
}

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

			// Discover ports from docker-compose + workspace config
			var ports []discoveredPort
			if err := conn.Call("workspace.discover-ports", map[string]any{"id": workspaceID}, &ports); err != nil {
				return fmt.Errorf("nexus spotlight start: port discovery failed: %w", err)
			}
			if len(ports) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no ports discovered")
				return nil
			}

			// Load profile for SSH tunneling
			p, err := profile.LoadDefault()
			if err != nil {
				return fmt.Errorf("nexus spotlight start: %w", err)
			}
			if err := stopClientActiveSpotlight(conn, p, cmd.OutOrStdout()); err != nil {
				return fmt.Errorf("nexus spotlight start: stop previous spotlight: %w", err)
			}

		// Create daemon-side spotlight forwards + SSH tunnels for each port.
		// On any failure, roll back the whole workspace spotlight via spotlight.stop.
		forwarded := 0
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

			targetHost := "127.0.0.1"
			targetPort := port.RemotePort
			if result.Forward != nil {
				if result.Forward.TargetHost != "" {
					targetHost = result.Forward.TargetHost
				}
				if result.Forward.RemotePort > 0 {
					targetPort = result.Forward.RemotePort
				}
			}

			boundPort, err := openSSHTunnel(p, port.LocalPort, targetHost, targetPort)
			if err != nil {
				_ = conn.Call("spotlight.stop", map[string]any{"workspaceId": workspaceID}, nil)
				return fmt.Errorf("%d/%s: tunnel failed: %w", port.LocalPort, port.Service, err)
			}

			label := port.Service
			if label == "" {
				label = fmt.Sprintf(":%d", port.RemotePort)
			}
			if boundPort != port.LocalPort {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s → localhost:%d (remapped from :%d, port in use)\n", label, boundPort, port.LocalPort)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s → localhost:%d\n", label, boundPort)
			}
			forwarded++
		}

		if err := persistClientActiveSpotlight(p, workspaceID); err != nil {
			return fmt.Errorf("persist spotlight client state: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "forwarded %d/%d ports\n", forwarded, len(ports))
			return nil
		},
	}

	return cmd
}

// openSSHTunnel creates an SSH tunnel: client localhost:localPort → daemon targetHost:targetPort.
// If localPort is already bound by another spotlight forward (multi-tenant scenario where two
// workspaces expose the same port), an ephemeral local port is selected instead.
// Returns the actual local port that was bound.
func openSSHTunnel(p *profile.Profile, localPort int, targetHost string, targetPort int) (int, error) {
	tunnelCache.mu.Lock()
	defer tunnelCache.mu.Unlock()

	if tunnelCache.forwards == nil {
		tunnelCache.forwards = make(map[int]*sshtunnel.Manager)
	}

	if existing, exists := tunnelCache.forwards[localPort]; exists {
		// Port is already forwarded by another entry in this session — reuse.
		return existing.LocalPort(), nil
	}

	tm := sshtunnel.NewWithTarget(p.Host, targetHost, targetPort, p.SSHPort)

	// Try the desired local port first; fall back to ephemeral if it's in use.
	bound, err := tm.EnsureWithLocalPort(localPort)
	if err != nil {
		// Port likely in use by another process (different workspace/service).
		// Let the OS pick a free port so this workspace can still be reached.
		tmFallback := sshtunnel.NewWithTarget(p.Host, targetHost, targetPort, p.SSHPort)
		bound, err = tmFallback.EnsureWithLocalPort(0)
		if err != nil {
			return 0, err
		}
		tm = tmFallback
	}

	tunnelCache.forwards[bound] = tm
	return bound, nil
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
	closeAllCachedTunnels()
	return saveSpotlightClientState(state)
}

func persistClientActiveSpotlight(p *profile.Profile, workspaceID string) error {
	state, err := loadSpotlightClientState()
	if err != nil {
		return err
	}
	key := spotlightProfileKey(p)
	if workspaceID == "" {
		delete(state.Profiles, key)
		return saveSpotlightClientState(state)
	}
	state.Profiles[key] = spotlightProfileState{WorkspaceID: workspaceID}
	return saveSpotlightClientState(state)
}

func closeAllCachedTunnels() {
	tunnelCache.mu.Lock()
	defer tunnelCache.mu.Unlock()
	for _, tm := range tunnelCache.forwards {
		_ = tm.Close()
	}
	tunnelCache.forwards = make(map[int]*sshtunnel.Manager)
}
