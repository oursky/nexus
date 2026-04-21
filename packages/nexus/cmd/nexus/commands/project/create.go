package project

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func createCommand() *cobra.Command {
	var name string
	var repoURL string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("nexus project create: --name is required")
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus project create: %w", err)
			}
			defer conn.Close()

			var result struct {
				Project struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					RepoURL string `json:"repoUrl"`
				} `json:"project"`
			}
			if err := rpc.Do(conn, "project.create", map[string]any{
				"name":    name,
				"repoUrl": repoURL,
			}, &result); err != nil {
				return fmt.Errorf("nexus project create: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created project %s (%s)\n", result.Project.Name, result.Project.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "project name (required)")
	cmd.Flags().StringVar(&repoURL, "repo", "", "repository URL")
	return cmd
}
