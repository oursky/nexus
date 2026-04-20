package spotlight

import (
	"fmt"
	"os"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func portRemoveCommand() *cobra.Command {
	var workspaceID string
	var forwardID string
	var force bool

	cmd := &cobra.Command{
		Use:     "remove",
		Aliases: []string{"rm"},
		Short:   "Remove a port forward from a workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			if workspaceID == "" || forwardID == "" {
				return fmt.Errorf("nexus spotlight port remove: --workspace and --forward-id are required")
			}

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

	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace ID (required)")
	cmd.Flags().StringVar(&forwardID, "forward-id", "", "forward ID to remove (required)")
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}
