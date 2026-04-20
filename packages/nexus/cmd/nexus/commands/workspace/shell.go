package workspace

import (
	"fmt"
	"os"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func shellCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell <workspace>",
		Short: "Open an interactive shell in a workspace",
		Args:  cobra.ExactArgs(1),
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
			// TODO: implement interactive shell via PTY RPC (requires terminal raw mode)
			fmt.Fprintf(os.Stderr, "shell command for workspace %s: not yet implemented in cobra commands\n", id)
			return nil
		},
	}
	return cmd
}
