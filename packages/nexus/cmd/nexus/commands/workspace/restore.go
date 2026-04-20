package workspace

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	"github.com/spf13/cobra"
)

func restoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <workspace>",
		Short: "Restore a workspace from a snapshot",
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
				Workspace workspace.Workspace `json:"workspace"`
			}
			if err := rpc.Do(conn, "workspace.restore", map[string]any{"id": id}, &result); err != nil {
				return fmt.Errorf("workspace restore: %w", err)
			}
			fmt.Printf("restored workspace %s\n", result.Workspace.ID)
			return nil
		},
	}
	return cmd
}
