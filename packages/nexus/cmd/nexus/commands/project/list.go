package project

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func listCommand() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus project list: %w", err)
			}
			defer conn.Close()

			var result struct {
				Projects []struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					RepoURL string `json:"repoUrl"`
				} `json:"projects"`
			}
			if err := rpc.Do(conn, "project.list", map[string]any{}, &result); err != nil {
				return fmt.Errorf("nexus project list: %w", err)
			}

			if jsonOut {
				rpc.PrintJSON(result.Projects)
				return nil
			}

			if len(result.Projects) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no projects")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s  %-20s  %s\n", "ID", "NAME", "REPO")
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s  %-20s  %s\n", "--------------------", "--------------------", "----")
			for _, p := range result.Projects {
				fmt.Fprintf(cmd.OutOrStdout(), "%-20s  %-20s  %s\n", p.ID, p.Name, p.RepoURL)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	return cmd
}
