package spotlight

import (
	"fmt"
	"os"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func portRemoveCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "remove <workspace-id> <forward-id>",
		Aliases: []string{"rm"},
		Short:   "Remove a port forward from a workspace",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceID := args[0]
			forwardID := args[1]

			if !force {
				fi, _ := os.Stdin.Stat()
				isTTY := (fi.Mode() & os.ModeCharDevice) != 0
				if isTTY {
					if !rpc.ConfirmPrompt(fmt.Sprintf("remove port forward %s?", forwardID)) {
						fmt.Fprintln(cmd.OutOrStdout(), "aborted")
						return nil
					}
				}
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus spotlight port remove: %w", err)
			}
			defer conn.Close()

			if err := rpc.Do(conn, "workspace.ports.remove", map[string]any{
				"workspaceId": workspaceID,
				"forwardId":   forwardID,
			}, nil); err != nil {
				return fmt.Errorf("nexus spotlight port remove: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed port forward %s\n", forwardID)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}
