package spotlight

import (
	"fmt"
	"os"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

func stopCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "stop <workspace-id>",
		Short: "Stop spotlight for a workspace (closes all forwarded ports)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceID := args[0]

			if !force {
				fi, _ := os.Stdin.Stat()
				isTTY := (fi.Mode() & os.ModeCharDevice) != 0
				if isTTY {
					if !rpc.ConfirmPrompt(fmt.Sprintf("stop spotlight for workspace %s?", workspaceID)) {
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

			if err := rpc.Do(conn, "spotlight.stop", map[string]any{"workspaceId": workspaceID}, nil); err != nil {
				return fmt.Errorf("nexus spotlight stop: %w", err)
			}
			clearClientSpotlightState(workspaceID)
			closeAllCachedTunnels()
			fmt.Fprintf(cmd.OutOrStdout(), "stopped spotlight for workspace %s\n", workspaceID)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}

// clearClientSpotlightState removes the spotlight state entry for any profile
// that currently tracks the given workspaceID.
func clearClientSpotlightState(workspaceID string) {
	p, err := profile.LoadDefault()
	if err != nil {
		return
	}
	state, err := loadSpotlightClientState()
	if err != nil {
		return
	}
	key := spotlightProfileKey(p)
	if entry, ok := state.Profiles[key]; ok && entry.WorkspaceID == workspaceID {
		delete(state.Profiles, key)
		_ = saveSpotlightClientState(state)
	}
}
