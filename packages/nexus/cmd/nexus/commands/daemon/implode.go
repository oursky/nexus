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

// ImplodePrivilegedFn, if non-nil, runs the privileged half of implode
// (removing the bridge, iptables rules, VM assets, installed binaries, etc.).
// On Linux this is wired to runImplodePrivileged by the main package.
var ImplodePrivilegedFn func(w io.Writer) error

func implodeCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "implode",
		Short: "Tear down all Nexus daemon state and host prerequisites",
		Long: `Implode removes every trace of the Nexus daemon from this machine:

  • Stops the running daemon
  • Removes all workspace runtime state (DB, socket, Firecracker VM dirs)
  • Removes the auth token
  • Removes the stored connection profile
  • [Linux] Tears down the nexusbr0 bridge and TAP interfaces
  • [Linux] Removes iptables/sysctl configuration added by daemon setup
  • [Linux] Removes ~/.local/bin/nexus-tap-helper and ~/.local/bin/firecracker
  • [Linux] Removes the VM kernel and rootfs from /var/lib/nexus/

After implode, running ` + "`nexus daemon start`" + ` will re-provision
everything from scratch.`,
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
			stopCmd.SetArgs([]string{}) // prevent cobra re-parsing os.Args
			_ = stopCmd.Execute()

			// 2. Remove user-space state (no sudo needed).
			if err := implodeUserState(w); err != nil {
				fmt.Fprintf(w, "warning: user-space cleanup: %v\n", err)
			}

			// 3. Privileged cleanup (Linux: bridge, iptables, VM assets, binaries).
			if ImplodePrivilegedFn != nil {
				if err := ImplodePrivilegedFn(w); err != nil {
					return fmt.Errorf("implode: privileged cleanup: %w", err)
				}
			}

			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "✓ Nexus daemon state removed.")
			fmt.Fprintln(w, "  To start fresh: nexus daemon start")
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

	stateDir := filepath.Join(xdgState, "nexus")
	dataDir := filepath.Join(xdgData, "nexus")

	for _, dir := range []string{stateDir, dataDir} {
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
