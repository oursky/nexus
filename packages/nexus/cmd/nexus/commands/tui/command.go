package tuicmd

import (
	nexustui "github.com/oursky/nexus/packages/nexus/internal/tui"
	"github.com/spf13/cobra"
)

// RunDefault launches the TUI with the same defaults as `nexus tui` (no flags).
func RunDefault() error {
	return nexustui.Run()
}

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI for workspaces",
		Long: `Launch the Nexus interactive terminal UI.

The workspace table is the home base — always visible. Use 't' to attach
a terminal to the selected workspace (nexus workspace shell). Session tabs
appear in a persistent bar after the first attachment.

If no daemon profile exists, a connect wizard will guide you through setup.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return nexustui.Run()
		},
	}

	return cmd
}
