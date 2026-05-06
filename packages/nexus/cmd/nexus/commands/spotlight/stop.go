package spotlight

import (
	"fmt"
	"os"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/sshtunnel"
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

			// Capture current daemon forwards first so we can evict local listeners on
			// their local ports even after daemon-side stop clears the records.
			type forward struct {
				LocalPort int `json:"localPort"`
			}
			type discoveredPort struct {
				LocalPort int `json:"localPort"`
			}
			var forwards struct {
				Forwards []forward `json:"forwards"`
			}
			var discovered []discoveredPort
			_ = rpc.Do(conn, "spotlight.list", map[string]any{"workspaceId": workspaceID}, &forwards)
			_ = rpc.Do(conn, "workspace.discover-ports", map[string]any{"id": workspaceID}, &discovered)

			if err := rpc.Do(conn, "spotlight.stop", map[string]any{"workspaceId": workspaceID}, nil); err != nil {
				return fmt.Errorf("nexus spotlight stop: %w", err)
			}

			ports := make([]int, 0, len(forwards.Forwards)+len(discovered))
			seenPorts := map[int]struct{}{}
			for _, f := range forwards.Forwards {
				if f.LocalPort <= 0 {
					continue
				}
				if _, exists := seenPorts[f.LocalPort]; exists {
					continue
				}
				seenPorts[f.LocalPort] = struct{}{}
				ports = append(ports, f.LocalPort)
			}
			for _, d := range discovered {
				if d.LocalPort <= 0 {
					continue
				}
				if _, exists := seenPorts[d.LocalPort]; exists {
					continue
				}
				seenPorts[d.LocalPort] = struct{}{}
				ports = append(ports, d.LocalPort)
			}
			if len(ports) > 0 {
				sshtunnel.CloseListenersOnPorts(ports)
			}
			pids := loadTunnelPIDsForWorkspace(workspaceID)
			clearClientSpotlightState(workspaceID)
			for _, pid := range pids {
				sshtunnel.CloseByPID(pid)
			}
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
