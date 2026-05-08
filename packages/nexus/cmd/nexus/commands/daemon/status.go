package daemon

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Nexus daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return err
			}
			defer conn.Close()

			var result map[string]any
			if err := rpc.Do(conn, "daemon.status", nil, &result); err != nil {
				return fmt.Errorf("daemon status: %w", err)
			}

			if macTunnel, ok := result["macTunnel"].(map[string]any); ok {
				fmt.Printf("Mac tunnel: port=%v user=%v path=%v\n",
					macTunnel["port"], macTunnel["macUser"], macTunnel["macPath"])
			} else {
				fmt.Println("Mac tunnel: not configured")
			}
			return nil
		},
	}
}
