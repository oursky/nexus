package daemon

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oursky/nexus/packages/nexus/internal/buildinfo"
)

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version of this nexus binary",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(buildinfo.Info())
		},
	}
}
