package daemon

import (
	"fmt"

	"github.com/spf13/cobra"
)

func statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Nexus daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("daemon status: not yet implemented")
		},
	}
}
