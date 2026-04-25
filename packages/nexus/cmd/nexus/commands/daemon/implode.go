package daemon

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// ImplodeUserCleanupFn, if non-nil, runs unprivileged driver-specific cleanup
// before user-state directories are removed (e.g. killing libkrun/passt PIDs).
var ImplodeUserCleanupFn func(w io.Writer)

func implodeCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "implode",
		Short: "Tear down all Nexus daemon state",
		Long: `Implode removes every trace of the Nexus daemon from this machine:

  • Stops the running daemon
  • Kills any lingering libkrun/passt child processes
  • Removes all workspace runtime state (DB, socket, VM dirs)
  • Removes the auth token and stored connection profile
  • Removes libkrun, passt, kernel, and rootfs from ~/.local/share/nexus/

Run ` + "`nexus daemon start`" + ` afterward — bootstrap runs automatically.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()

			if !force {
				fmt.Fprintln(w, "This will permanently remove all Nexus daemon state from this machine.")
				fmt.Fprintln(w, "Type \"yes\" to confirm:")
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				if strings.TrimSpace(scanner.Text()) != "yes" {
					fmt.Fprintln(w, "Aborted.")
					return nil
				}
			}

			// 1. Stop the daemon.
			fmt.Fprintln(w, "==> Stopping daemon...")
			stopCmd := stopCommand()
			stopCmd.SetOut(w)
			stopCmd.SetErr(cmd.ErrOrStderr())
			stopCmd.SetArgs([]string{})
			_ = stopCmd.Execute()

			// 2. Driver-specific unprivileged cleanup (kill libkrun/passt orphans).
			if ImplodeUserCleanupFn != nil {
				ImplodeUserCleanupFn(w)
			}

			// 3. Remove user-space state.
			if err := implodeUserState(w); err != nil {
				fmt.Fprintf(w, "warning: user-space cleanup: %v\n", err)
			}

			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "✓ Nexus daemon state removed.")
			fmt.Fprintln(w, "  Run: nexus daemon start")
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	return cmd
}

// implodeUserState removes all user-space nexus state directories and files.
func implodeUserState(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	xdgState := os.Getenv("XDG_STATE_HOME")
	if xdgState == "" {
		xdgState = filepath.Join(home, ".local", "state")
	}
	xdgData := os.Getenv("XDG_DATA_HOME")
	if xdgData == "" {
		xdgData = filepath.Join(home, ".local", "share")
	}

	for _, dir := range []string{
		filepath.Join(xdgState, "nexus"),
		filepath.Join(xdgData, "nexus"),
	} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		fmt.Fprintf(w, "==> Removing %s\n", dir)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	return nil
}
