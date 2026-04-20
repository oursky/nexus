package workspace

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func checkoutCommand() *cobra.Command {
	var targetRef string

	cmd := &cobra.Command{
		Use:   "checkout <workspace>",
		Short: "Checkout a different ref in a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if targetRef == "" {
				return fmt.Errorf("--ref is required")
			}
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return err
			}
			defer conn.Close()
			id, err := rpc.ResolveWorkspaceID(cmd.Context(), conn, args[0])
			if err != nil {
				return err
			}
			params := map[string]any{
				"id": id,
				"spec": map[string]any{
					"targetRef": targetRef,
				},
			}
			if err := rpc.Do(conn, "workspace.checkout", params, nil); err != nil {
				return fmt.Errorf("workspace checkout: %w", err)
			}
			fmt.Printf("checked out %s in workspace %s\n", targetRef, id)
			return nil
		},
	}
	cmd.Flags().StringVar(&targetRef, "ref", "", "target ref (required)")
	return cmd
}
