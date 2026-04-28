package daemon

import (
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
	"github.com/spf13/cobra"
)

// Command returns the "daemon" cobra command group with all subcommands registered.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Nexus daemon",
	}
	cmd.AddCommand(
		start.Command(),
		stopCommand(),
		statusCommand(),
		tokenCommand(),
		connectCommand(),
		disconnectCommand(),
		profileCommand(),
		implodeCommand(),
		checkCommand(),
		versionCommand(),
	)
	return cmd
}
