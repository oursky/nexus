package daemon

import (
	"fmt"

	"github.com/spf13/cobra"
)

func stopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running Nexus daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("daemon stop: not yet implemented")
		},
	}
}
