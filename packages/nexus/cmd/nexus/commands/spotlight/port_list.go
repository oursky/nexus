package spotlight

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/spf13/cobra"
)

func portListCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:     "list <workspace-id>",
		Aliases: []string{"ls"},
		Short:   "List port forwards for a workspace",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceID := args[0]

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus spotlight port list: %w", err)
			}
			defer conn.Close()

			var result struct {
				Forwards []*spotlight.Forward `json:"forwards"`
			}
			if err := rpc.Do(conn, "workspace.ports.list", map[string]any{
				"workspaceId": workspaceID,
			}, &result); err != nil {
				return fmt.Errorf("nexus spotlight port list: %w", err)
			}

			if asJSON {
				rpc.PrintJSON(result.Forwards)
				return nil
			}

			if len(result.Forwards) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no ports")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-10s  %-10s  %-10s  %s\n", "LOCAL", "REMOTE", "PROTOCOL", "STATE")
			fmt.Fprintf(cmd.OutOrStdout(), "%-10s  %-10s  %-10s  %s\n", "----------", "----------", "----------", "-----")
			for _, fwd := range result.Forwards {
				proto := fwd.Protocol
				if proto == "" {
					proto = "tcp"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-10d  %-10d  %-10s  %s\n", fwd.LocalPort, fwd.RemotePort, proto, fwd.State)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return cmd
}
