package tuicmd

import (
	nexustui "github.com/oursky/nexus/packages/nexus/internal/tui"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI for workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nexustui.Run()
		},
	}
}
