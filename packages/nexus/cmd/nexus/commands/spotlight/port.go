package spotlight

import (
	"github.com/spf13/cobra"
)

func portCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "port",
		Short: "Manage port forwards for a workspace",
	}
	cmd.AddCommand(
		portListCommand(),
		portAddCommand(),
		portRemoveCommand(),
	)
	return cmd
}
