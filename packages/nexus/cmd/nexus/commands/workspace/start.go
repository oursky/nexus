package workspace

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
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

			// Discover ports from docker-compose + workspace config (informational only)
			var ports []discoveredPort
			if err := rpc.Do(conn, "workspace.discover-ports", map[string]any{"id": wsID}, &ports); err != nil {
				// Non-fatal: port discovery failure shouldn't block workspace start
				return nil
			}

			if len(ports) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "discovered %d port(s):", len(ports))
				for _, p := range ports {
					label := p.Service
					if label == "" {
						label = fmt.Sprintf(":%d", p.RemotePort)
					}
					fmt.Fprintf(cmd.OutOrStdout(), " %s/%d", label, p.LocalPort)
				}
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintf(cmd.OutOrStdout(), "use 'nexus spotlight start' to forward ports\n")
			}
			return nil
		},
	}
}
