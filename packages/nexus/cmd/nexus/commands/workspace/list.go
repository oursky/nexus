package workspace

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	"github.com/spf13/cobra"
)

func listCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace list: %w", err)
			}
			defer conn.Close()

			var result struct {
				Workspaces []workspace.Workspace `json:"workspaces"`
			}
			if err := rpc.Do(conn, "workspace.list", map[string]any{}, &result); err != nil {
				return fmt.Errorf("nexus workspace list: %w", err)
			}

			if len(result.Workspaces) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no workspaces")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %-20s  %-10s  %-10s  %s\n", "ID", "NAME", "STATE", "BACKEND", "WORKTREE")
			fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %-20s  %-10s  %-10s  %s\n",
				"------------------------------------", "--------------------",
				"----------", "----------", "--------")
			for _, ws := range result.Workspaces {
				wt := "—"
				fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %-20s  %-10s  %-10s  %s\n",
					ws.ID, ws.WorkspaceName, ws.State, ws.Backend, wt)
			}
			return nil
		},
	}
}

func infoCommand() *cobra.Command {
	var jsonFlag bool

	cmd := &cobra.Command{
		Use:   "info <id|name>",
		Short: "Show workspace details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nameOrID := args[0]

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace info: %w", err)
			}
			defer conn.Close()

			wsID, err := rpc.ResolveWorkspaceID(cmd.Context(), conn, nameOrID)
			if err != nil {
				return fmt.Errorf("nexus workspace info: %w", err)
			}

			var result struct {
				Workspace struct {
					ID                string `json:"id"`
					WorkspaceName     string `json:"workspaceName"`
					State             string `json:"state"`
					Repo              string `json:"repo"`
					Ref               string `json:"ref"`
					Backend           string `json:"backend"`
					RootPath          string `json:"rootPath"`
					AgentProfile      string `json:"agentProfile"`
					ParentWorkspaceID string `json:"parentWorkspaceId"`
					LineageRootID     string `json:"lineageRootId"`
					ProjectID         string `json:"projectId"`
				} `json:"workspace"`
			}
			if err := rpc.Do(conn, "workspace.info", map[string]any{"id": wsID}, &result); err != nil {
				return fmt.Errorf("nexus workspace info: %w", err)
			}

			if jsonFlag {
				rpc.PrintJSON(result.Workspace)
				return nil
			}

			ws := result.Workspace
			fmt.Fprintf(cmd.OutOrStdout(), "id:                 %s\n", ws.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "name:               %s\n", ws.WorkspaceName)
			fmt.Fprintf(cmd.OutOrStdout(), "state:              %s\n", ws.State)
			fmt.Fprintf(cmd.OutOrStdout(), "repo:               %s\n", ws.Repo)
			fmt.Fprintf(cmd.OutOrStdout(), "ref:                %s\n", ws.Ref)
			fmt.Fprintf(cmd.OutOrStdout(), "backend:            %s\n", ws.Backend)
			fmt.Fprintf(cmd.OutOrStdout(), "rootPath:           %s\n", ws.RootPath)
			fmt.Fprintf(cmd.OutOrStdout(), "agentProfile:       %s\n", ws.AgentProfile)
			if ws.ProjectID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "projectId:          %s\n", ws.ProjectID)
			}
			if ws.ParentWorkspaceID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "parentWorkspaceId:  %s\n", ws.ParentWorkspaceID)
			}
			if ws.LineageRootID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "lineageRootId:      %s\n", ws.LineageRootID)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	return cmd
}
