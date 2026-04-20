package spotlight

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/spf13/cobra"
)

func listCommand() *cobra.Command {
	var workspaceID string
	var asJSON bool

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List spotlight forwards",
		RunE: func(cmd *cobra.Command, args []string) error {
			if workspaceID == "" {
				return fmt.Errorf("nexus spotlight list: --workspace is required")
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus spotlight list: %w", err)
			}
			defer conn.Close()

			var result struct {
				Forwards []*spotlight.Forward `json:"forwards"`
			}
			if err := rpc.Do(conn, "spotlight.list", map[string]any{
				"workspaceId": workspaceID,
			}, &result); err != nil {
				return fmt.Errorf("nexus spotlight list: %w", err)
			}

			if asJSON {
				rpc.PrintJSON(result.Forwards)
				return nil
			}

			if len(result.Forwards) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no forwards")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %-36s  %-10s  %-10s  %s\n", "ID", "WORKSPACE", "LOCAL", "REMOTE", "STATE")
			fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %-36s  %-10s  %-10s  %s\n",
				"------------------------------------", "------------------------------------",
				"----------", "----------", "-----")
			for _, fwd := range result.Forwards {
				fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %-36s  %-10d  %-10d  %s\n",
					fwd.ID, fwd.WorkspaceID, fwd.LocalPort, fwd.RemotePort, fwd.State)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceID, "workspace", "", "filter by workspace ID (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return cmd
}
