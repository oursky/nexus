package spotlight

import (
	"fmt"
	"os"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func stopCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "stop <id>",
		Aliases: []string{"close"},
		Short:   "Stop a spotlight forward",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			if !force {
				fi, _ := os.Stdin.Stat()
				isTTY := (fi.Mode() & os.ModeCharDevice) != 0
				if isTTY {
					if !rpc.ConfirmPrompt(fmt.Sprintf("stop spotlight forward %s?", id)) {
						fmt.Fprintln(cmd.OutOrStdout(), "aborted")
						return nil
					}
				}
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus spotlight stop: %w", err)
			}
			defer conn.Close()

			if err := rpc.Do(conn, "spotlight.stop", map[string]any{"id": id}, nil); err != nil {
				return fmt.Errorf("nexus spotlight stop: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stopped spotlight forward %s\n", id)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}
