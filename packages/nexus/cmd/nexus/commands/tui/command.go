package tuicmd

import (
	nexustui "github.com/oursky/nexus/packages/nexus/internal/tui"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	var autoAttach bool

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI for workspaces",
		Long: `Launch the Nexus interactive terminal UI.

The workspace table is the home base — always visible. Use 't' to attach
a terminal to the selected workspace (nexus workspace shell). Session tabs
appear in a persistent bar after the first attachment.

Flags:
  --auto-attach   Re-enter the last workspace shell on startup (if running).
                  NOT the default; session state is always restored from disk.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return nexustui.Run(nexustui.Options{AutoAttach: autoAttach})
		},
	}

	cmd.Flags().BoolVar(&autoAttach, "auto-attach", false,
		"re-attach to the last active workspace on startup (only if it is running)")

	return cmd
}
