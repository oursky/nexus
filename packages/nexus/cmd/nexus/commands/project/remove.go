package project

import (
	"fmt"
	"os"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func removeCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "remove <id|name>",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a project",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus project remove: %w", err)
			}
			defer conn.Close()

			id, err := rpc.ResolveProjectID(cmd.Context(), conn, args[0])
			if err != nil {
				return fmt.Errorf("nexus project remove: %w", err)
			}

			if !force {
				fi, _ := os.Stdin.Stat()
				isTTY := (fi.Mode() & os.ModeCharDevice) != 0
				if isTTY {
					if !rpc.ConfirmPrompt(fmt.Sprintf("remove project %s?", id)) {
						fmt.Fprintln(cmd.OutOrStdout(), "aborted")
						return nil
					}
				}
			}

			if err := rpc.Do(conn, "project.remove", map[string]any{"id": id}, nil); err != nil {
				return fmt.Errorf("nexus project remove: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed project %s\n", id)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}
