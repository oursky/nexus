package workspace

import (
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "Manage workspaces",
	}
	cmd.AddCommand(
		listCommand(),
		createCommand(),
		startCommand(),
		stopCommand(),
		removeCommand(),
		infoCommand(),
		forkCommand(),
		restoreCommand(),
		readyCommand(),
		shellCommand(),
		runCommand(),
		portalCommand(),
	)
	return cmd
}
