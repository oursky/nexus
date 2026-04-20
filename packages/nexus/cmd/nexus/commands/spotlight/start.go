package spotlight

import (
	"fmt"
	"sync"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/sshtunnel"
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
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus spotlight start: %w", err)
			}
			defer conn.Close()

			// Discover ports from docker-compose + workspace config
			var ports []discoveredPort
			if err := rpc.Do(conn, "workspace.discover-ports", map[string]any{"id": workspaceID}, &ports); err != nil {
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

			// Create daemon-side spotlight forwards + SSH tunnels for each port
			forwarded := 0
			for _, port := range ports {
				// Create daemon-side forward
				spec := spotlight.ExposeSpec{
					WorkspaceID: workspaceID,
					LocalPort:   port.LocalPort,
					RemotePort:  port.RemotePort,
					Protocol:    port.Protocol,
				}
				var result struct {
					Forward *spotlight.Forward `json:"forward"`
				}
				if err := rpc.Do(conn, "spotlight.start", map[string]any{
					"workspaceId": workspaceID,
					"spec":        spec,
				}, &result); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  %d/%s: daemon forward failed: %v\n", port.LocalPort, port.Service, err)
					continue
				}

				// Open SSH tunnel to client
				if err := openSSHTunnel(p, port.LocalPort, port.RemotePort); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  %d/%s: tunnel failed: %v\n", port.LocalPort, port.Service, err)
					continue
				}

				label := port.Service
				if label == "" {
					label = fmt.Sprintf(":%d", port.RemotePort)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %s → localhost:%d\n", label, port.LocalPort)
				forwarded++
			}

			fmt.Fprintf(cmd.OutOrStdout(), "forwarded %d/%d ports\n", forwarded, len(ports))
			return nil
		},
	}

	return cmd
}

// openSSHTunnel creates an SSH tunnel: client localhost:localPort → daemon localhost:remotePort.
func openSSHTunnel(p *profile.Profile, localPort, remotePort int) error {
	tunnelCache.mu.Lock()
	defer tunnelCache.mu.Unlock()

	if tunnelCache.forwards == nil {
		tunnelCache.forwards = make(map[int]*sshtunnel.Manager)
	}

	if _, exists := tunnelCache.forwards[localPort]; exists {
		return nil // already tunnelling
	}

	tm := sshtunnel.New(p.Host, remotePort, p.SSHPort)
	if _, err := tm.EnsureWithLocalPort(localPort); err != nil {
		return err
	}

	tunnelCache.forwards[localPort] = tm
	return nil
}
