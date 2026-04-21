package project

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func getCommand() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "get <id|name>",
		Short: "Show project details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus project get: %w", err)
			}
			defer conn.Close()

			id, err := rpc.ResolveProjectID(cmd.Context(), conn, args[0])
			if err != nil {
				return fmt.Errorf("nexus project get: %w", err)
			}

			var result struct {
				Project struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					RepoURL string `json:"repoUrl"`
				} `json:"project"`
			}
			if err := rpc.Do(conn, "project.get", map[string]any{"id": id}, &result); err != nil {
				return fmt.Errorf("nexus project get: %w", err)
			}

			if jsonOut {
				rpc.PrintJSON(result.Project)
				return nil
			}

			p := result.Project
			fmt.Fprintf(cmd.OutOrStdout(), "id:      %s\n", p.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "name:    %s\n", p.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "repoUrl: %s\n", p.RepoURL)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	return cmd
}
