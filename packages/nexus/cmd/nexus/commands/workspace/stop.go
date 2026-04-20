package workspace

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func stopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id>",
		Short: "Stop a running workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace stop: %w", err)
			}
			defer conn.Close()

			if err := rpc.Do(conn, "workspace.stop", map[string]any{"id": args[0]}, nil); err != nil {
				return fmt.Errorf("nexus workspace stop: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stopped workspace %s\n", args[0])
			return nil
		},
	}
}
