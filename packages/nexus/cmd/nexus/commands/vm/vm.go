package vm

import (
	"github.com/spf13/cobra"
)

// Command returns the "vm" cobra command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vm",
		Short: "Manage VM assets",
	}
	cmd.AddCommand(bakeCommand())
	return cmd
}
