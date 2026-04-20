package workspace

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

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

			if err := rpc.Do(conn, "workspace.start", map[string]any{"id": args[0]}, nil); err != nil {
				return fmt.Errorf("nexus workspace start: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "started workspace %s\n", args[0])
			return nil
		},
	}
}
