package spotlight

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/spf13/cobra"
)

func portAddCommand() *cobra.Command {
	var port int
	var remotePort int
	var protocol string

	cmd := &cobra.Command{
		Use:   "add <workspace-id>",
		Short: "Add a port forward to a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceID := args[0]
			if port == 0 {
				return fmt.Errorf("nexus spotlight port add: --port is required")
			}
			if remotePort == 0 {
				remotePort = port
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus spotlight port add: %w", err)
			}
			defer conn.Close()

			spec := spotlight.ExposeSpec{
				WorkspaceID: workspaceID,
				LocalPort:   port,
				RemotePort:  remotePort,
				Protocol:    protocol,
			}
			var result struct {
				Forward *spotlight.Forward `json:"forward"`
			}
			if err := rpc.Do(conn, "workspace.ports.add", map[string]any{
				"workspaceId": workspaceID,
				"spec":        spec,
			}, &result); err != nil {
				return fmt.Errorf("nexus spotlight port add: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "port %d exposed\n", port)
			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 0, "local port to expose (required)")
	cmd.Flags().IntVar(&remotePort, "remote-port", 0, "remote port (defaults to --port)")
	cmd.Flags().StringVar(&protocol, "protocol", "", "protocol (tcp/http)")
	return cmd
}
