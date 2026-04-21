package workspace

import (
	"fmt"
	"os"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func removeCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "remove <id>",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a workspace",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace remove: %w", err)
			}
			defer conn.Close()

			if !force {
				fi, _ := os.Stdin.Stat()
				isTTY := (fi.Mode() & os.ModeCharDevice) != 0
				if isTTY {
					if !rpc.ConfirmPrompt(fmt.Sprintf("remove workspace %s?", args[0])) {
						fmt.Fprintln(cmd.OutOrStdout(), "aborted")
						return nil
					}
				}
			}

			if err := rpc.Do(conn, "workspace.remove", map[string]any{"id": args[0]}, nil); err != nil {
				return fmt.Errorf("nexus workspace remove: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed workspace %s\n", args[0])
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}
