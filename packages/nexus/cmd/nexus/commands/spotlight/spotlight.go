package spotlight

import (
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "spotlight",
		Aliases: []string{"spot"},
		Short:   "Manage spotlight port forwards",
	}
	cmd.AddCommand(
		startCommand(),
		listCommand(),
		stopCommand(),
		portCommand(),
	)
	return cmd
}
