//go:build !linux && !darwin

package vm

import (
	"fmt"

	"github.com/spf13/cobra"
)

func bakeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "bake",
		Short: "Pre-bake developer tools into the base rootfs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("vm bake is only supported on Linux and macOS")
		},
	}
}
