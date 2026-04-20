package workspace

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func readyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ready <workspace>",
		Short: "Check if a workspace is ready",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return err
			}
			defer conn.Close()
			id, err := rpc.ResolveWorkspaceID(cmd.Context(), conn, args[0])
			if err != nil {
				return err
			}
			var result struct {
				Ready bool `json:"ready"`
			}
			if err := rpc.Do(conn, "workspace.ready", map[string]any{"id": id}, &result); err != nil {
				return fmt.Errorf("workspace ready: %w", err)
			}
			if result.Ready {
				fmt.Println("ready")
			} else {
				fmt.Println("not ready")
			}
			return nil
		},
	}
	return cmd
}
