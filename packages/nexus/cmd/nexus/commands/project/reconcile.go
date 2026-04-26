package project

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func reconcileCommand() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Repair project/workspace relations and remove duplicate projects by repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus project reconcile: %w", err)
			}
			defer conn.Close()

			var result struct {
				UpdatedWorkspaces int      `json:"updatedWorkspaces"`
				RemovedProjects   int      `json:"removedProjects"`
				CreatedProjects   int      `json:"createdProjects"`
				CanonicalProjects []string `json:"canonicalProjects"`
			}
			if err := rpc.Do(conn, "project.reconcile", map[string]any{}, &result); err != nil {
				return fmt.Errorf("nexus project reconcile: %w", err)
			}

			if jsonOut {
				rpc.PrintJSON(result)
				return nil
			}

			fmt.Fprintf(
				cmd.OutOrStdout(),
				"reconciled projects: updatedWorkspaces=%d removedProjects=%d createdProjects=%d canonical=%d\n",
				result.UpdatedWorkspaces,
				result.RemovedProjects,
				result.CreatedProjects,
				len(result.CanonicalProjects),
			)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	return cmd
}
