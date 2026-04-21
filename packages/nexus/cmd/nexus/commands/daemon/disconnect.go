package daemon

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

func disconnectCommand() *cobra.Command {
	var removeProfile bool

	cmd := &cobra.Command{
		Use:   "disconnect",
		Short: "Disconnect from the remote daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if removeProfile {
				if err := profile.DeleteDefault(); err != nil {
					return fmt.Errorf("remove profile: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Profile removed and disconnected")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Disconnected (profile kept)")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&removeProfile, "remove", false, "also remove the saved profile")
	return cmd
}
