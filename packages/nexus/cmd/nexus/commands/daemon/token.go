package daemon

import (
	"fmt"

	"github.com/inizio/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/spf13/cobra"
)

func tokenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print the daemon bearer token",
		Long: `Print the bearer token the daemon uses for network authentication.

The token is loaded from the OS keyring (Secret Service on Linux) or the
fallback file (~/.config/nexus/daemon-token). If no token exists yet, a new
one is generated and persisted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := tokenstore.LoadOrGenerate()
			if err != nil {
				return fmt.Errorf("daemon token: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), token)
			return nil
		},
	}
}
