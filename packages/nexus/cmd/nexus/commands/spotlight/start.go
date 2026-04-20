package spotlight

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/spf13/cobra"
)

func startCommand() *cobra.Command {
	var workspaceID string
	var localPort int
	var remotePort int
	var protocol string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a spotlight port forward",
		RunE: func(cmd *cobra.Command, args []string) error {
			if workspaceID == "" || localPort == 0 {
				return fmt.Errorf("nexus spotlight start: --workspace and --local-port are required")
			}
			if remotePort == 0 {
				remotePort = localPort
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus spotlight start: %w", err)
			}
			defer conn.Close()

			spec := spotlight.ExposeSpec{
				WorkspaceID: workspaceID,
				LocalPort:   localPort,
				RemotePort:  remotePort,
				Protocol:    protocol,
			}
			var result struct {
				Forward *spotlight.Forward `json:"forward"`
			}
			if err := rpc.Do(conn, "spotlight.start", map[string]any{
				"workspaceId": workspaceID,
				"spec":        spec,
			}, &result); err != nil {
				return fmt.Errorf("nexus spotlight start: %w", err)
			}
			if result.Forward != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "spotlight started (%s)\n", result.Forward.ID)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "spotlight started")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace ID (required)")
	cmd.Flags().IntVar(&localPort, "local-port", 0, "local port to expose (required)")
	cmd.Flags().IntVar(&remotePort, "remote-port", 0, "remote port (defaults to local-port)")
	cmd.Flags().StringVar(&protocol, "protocol", "", "protocol (tcp/http)")
	return cmd
}
