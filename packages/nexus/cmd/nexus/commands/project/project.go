package project

import (
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project",
		Aliases: []string{"proj"},
		Short:   "Manage projects",
	}
	cmd.AddCommand(
		listCommand(),
		createCommand(),
		getCommand(),
		removeCommand(),
	)
	return cmd
}
