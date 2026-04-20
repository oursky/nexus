package workspace

import (
	"fmt"
	"sync"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/sshtunnel"
	"github.com/spf13/cobra"
)

// portForwardCache holds active SSH port forwards keyed by remote port.
// Each forward tunnels Mac localhost:localPort → daemonHost localhost:remotePort.
var portForwardCache struct {
	mu       sync.Mutex
	forwards map[int]*sshtunnel.Manager // remotePort → tunnel
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
	return &cobra.Command{
		Use:   "start <id>",
		Short: "Start a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace start: %w", err)
			}
			defer conn.Close()

			wsID := args[0]

			if err := rpc.Do(conn, "workspace.start", map[string]any{"id": wsID}, nil); err != nil {
				return fmt.Errorf("nexus workspace start: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "started workspace %s\n", wsID)

			// Discover ports from docker-compose + workspace config
			var ports []discoveredPort
			if err := rpc.Do(conn, "workspace.discover-ports", map[string]any{"id": wsID}, &ports); err != nil {
				// Non-fatal: port discovery failure shouldn't block workspace start
				fmt.Fprintf(cmd.OutOrStdout(), "warning: port discovery failed: %v\n", err)
				return nil
			}

			if len(ports) == 0 {
				return nil
			}

			// Load profile for SSH host info
			p, err := profile.LoadDefault()
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "warning: cannot load profile for port forwards: %v\n", err)
				return nil
			}

			// Open SSH port forwards for each discovered port
			forwarded := 0
			for _, port := range ports {
				if err := openPortForward(p, port.RemotePort, port.LocalPort); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "warning: port %d forward failed: %v\n", port.LocalPort, err)
					continue
				}
				forwarded++
			}

			if forwarded > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "forwarded %d/%d ports to localhost\n", forwarded, len(ports))
			}
			return nil
		},
	}
}

// openPortForward creates an SSH tunnel from Mac localhost:localPort → daemonHost localhost:remotePort.
func openPortForward(p *profile.Profile, remotePort, localPort int) error {
	portForwardCache.mu.Lock()
	defer portForwardCache.mu.Unlock()

	if portForwardCache.forwards == nil {
		portForwardCache.forwards = make(map[int]*sshtunnel.Manager)
	}

	// Already forwarded?
	if _, exists := portForwardCache.forwards[remotePort]; exists {
		return nil
	}

	tm := sshtunnel.New(p.Host, remotePort, p.SSHPort)
	// Use the same local port as remote (1:1 mapping)
	actualLocal, err := tm.EnsureWithLocalPort(localPort)
	if err != nil {
		return err
	}

	portForwardCache.forwards[remotePort] = tm
	_ = actualLocal
	return nil
}
