package tuicmd

import (
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	nexustui "github.com/oursky/nexus/packages/nexus/internal/tui"
	"github.com/spf13/cobra"
)

// RunDefault launches the TUI with the same defaults as `nexus tui` (no flags).
func RunDefault() error {
	mux, err := rpc.EnsureMux()
	if err != nil {
		return err
	}
	defer mux.Close()
	return nexustui.Run(mux)
}

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI for workspaces",
		Long: `Launch the Nexus interactive terminal UI.

The workspace table is the home base — always visible. Use 't' to attach
a terminal to the selected workspace (nexus workspace shell). Session tabs
appear in a persistent bar after the first attachment.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			mux, err := rpc.EnsureMux()
			if err != nil {
				return err
			}
			defer mux.Close()
			return nexustui.Run(mux)
		},
	}

	return cmd
}