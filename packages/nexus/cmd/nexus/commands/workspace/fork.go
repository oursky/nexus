package workspace

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	"github.com/spf13/cobra"
)

func forkCommand() *cobra.Command {
	var childName string
	var childRef string

	cmd := &cobra.Command{
		Use:   "fork <workspace>",
		Short: "Fork a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if childRef == "" {
				return fmt.Errorf("--ref is required")
			}
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
			params := map[string]any{
				"id":                 id,
				"childWorkspaceName": childName,
				"childRef":           childRef,
			}
			if err := rpc.Do(conn, "workspace.fork", params, &result); err != nil {
				return fmt.Errorf("workspace fork: %w", err)
			}
			fmt.Printf("forked workspace %s  (id: %s)\n", result.Workspace.WorkspaceName, result.Workspace.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&childName, "name", "", "child workspace name (default: <parent>-fork)")
	cmd.Flags().StringVar(&childRef, "ref", "", "branch/ref for the fork (required)")
	return cmd
}
