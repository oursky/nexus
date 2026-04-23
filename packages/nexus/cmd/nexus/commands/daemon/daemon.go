package daemon

import (
	"github.com/spf13/cobra"
)

// Command returns the "daemon" cobra command group with all subcommands registered.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Nexus daemon",
	}
	cmd.AddCommand(
		startCommand(),
		stopCommand(),
		statusCommand(),
		tokenCommand(),
		connectCommand(),
		disconnectCommand(),
		profileCommand(),
		implodeCommand(),
		checkCommand(),
	)
	return cmd
}
