package workspace

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func portalCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "portal <workspace>",
		Short: "Open a portal to a workspace",
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
			var result struct {
				Ports []int `json:"ports"`
			}
			if err := rpc.Do(conn, "workspace.portsList", map[string]any{"id": id}, &result); err != nil {
				return fmt.Errorf("workspace portal: %w", err)
			}
			if len(result.Ports) == 0 {
				fmt.Println("no tunnel ports configured")
			} else {
				for _, p := range result.Ports {
					fmt.Printf("port: %d\n", p)
				}
			}
			return nil
		},
	}
	return cmd
}
