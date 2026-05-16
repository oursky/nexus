package tuicmd

import (
	"fmt"
	"os"

	nexustui "github.com/oursky/nexus/packages/nexus/internal/tui"
	updatecmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/update"
	"github.com/spf13/cobra"
)

// RunDefault launches the TUI with the same defaults as `nexus tui` (no flags).
func RunDefault() error {
	startBackgroundUpdateCheck()
	return nexustui.Run(nexustui.Options{
		AutoAttach: false,
		Port:       defaultLocalPort,
	})
}

func Command() *cobra.Command {
	var autoAttach bool
	var port int

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI for workspaces",
		Long: `Launch the Nexus interactive terminal UI.

The workspace table is the home base — always visible. Use 't' to attach
a terminal to the selected workspace (nexus workspace shell). Session tabs
appear in a persistent bar after the first attachment.

Flags:
  --auto-attach   Re-enter the last workspace shell on startup (if running).
                  NOT the default; session state is always restored from disk.
  --port          Local daemon port to probe / start on first run (default: 7777).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			startBackgroundUpdateCheck()
			return nexustui.Run(nexustui.Options{AutoAttach: autoAttach, Port: port})
		},
	}

	cmd.Flags().BoolVar(&autoAttach, "auto-attach", false,
		"re-attach to the last active workspace on startup (only if it is running)")
	cmd.Flags().IntVar(&port, "port", defaultLocalPort,
		"local daemon port to probe/start on first run")

	return cmd
}

// startBackgroundUpdateCheck runs the update check in a goroutine.
// When complete, it prints a message to stderr if an update is available.
// The check is non-blocking and best-effort (failures are silent).
func startBackgroundUpdateCheck() {
	go func() {
		available, latest := updatecmd.IsUpdateAvailable("oursky/nexus")
		if available {
			fmt.Fprintf(os.Stderr, "\n✨ Nexus %s is available — run 'nexus update' to upgrade\n\n", latest)
		}
	}()
}
