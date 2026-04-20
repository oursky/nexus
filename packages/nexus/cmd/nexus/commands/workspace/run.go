package workspace

import (
	"fmt"
	"os"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func runCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <workspace> -- <command> [args...]",
		Short: "Run a command in a workspace",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return err
			}
			defer conn.Close()
			id, err := rpc.ResolveWorkspaceID(cmd.Context(), conn, args[0])
			if err != nil {
				return err
			}
			// TODO: implement run command via PTY RPC (requires terminal handling)
			fmt.Fprintf(os.Stderr, "run command for workspace %s: not yet implemented in cobra commands\n", id)
			return nil
		},
	}
	return cmd
}
