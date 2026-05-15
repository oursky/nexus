package daemon

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

// ImplodeUserCleanupFn, if non-nil, runs unprivileged driver-specific cleanup
// before user-state directories are removed (e.g. killing libkrun/passt PIDs).
var ImplodeUserCleanupFn func(w io.Writer)

func implodeCommand() *cobra.Command {
	var force, keepVMStore bool

	cmd := &cobra.Command{
		Use:   "implode",
		Short: "Tear down all Nexus daemon state and client tools on this machine",
		Long: `Implode destroys local Nexus installs and daemon state:

  • Stops production and developer daemons (default ports/socket paths)
  • Kills helper processes (libkrun, passt, pty-host)
  • Removes client profile + token, XDG/config state, ~/.nexus
  • Removes co-installed CLI binaries (nexus, pty-host, passt under common paths)
  • On Linux: clears /data/nexus contents Nexus uses for VM workspaces (unless --keep-vm-store)
  • On Linux: best-effort disables user systemd units matching *nexus*

Afterward, reinstall with install.sh or your package manager.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			stderr := cmd.ErrOrStderr()

			if !force {
				fmt.Fprintln(w, "This removes Nexus binaries, ~/.config/nexus, workspace VM store under")
				fmt.Fprintln(w, "/data/nexus (Linux), daemon DBs, sockets, SSH snippets under ~/.nexus, and systemd user units.")
				fmt.Fprintln(w, "This cannot be undone. Type \"yes\" to confirm:")
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				if strings.TrimSpace(strings.ToLower(scanner.Text())) != "yes" {
					fmt.Fprintln(w, "Aborted.")
					return nil
				}
			}

			fmt.Fprintln(w, "==> Stopping Nexus daemons...")
			stopAllLocalDaemons(w, stderr)

			if ImplodeUserCleanupFn != nil {
				ImplodeUserCleanupFn(w)
			}

			if err := profile.DeleteDefault(); err != nil {
				fmt.Fprintf(w, "warning: clear client profile: %v\n", err)
			}

			if err := implodeUserTrees(w); err != nil {
				fmt.Fprintf(w, "warning: user tree cleanup: %v\n", err)
			}

			if err := unlinkCommonSockets(w); err != nil {
				fmt.Fprintf(w, "warning: socket cleanup: %v\n", err)
			}

			if err := implodeRemoveBins(w); err != nil {
				fmt.Fprintf(w, "warning: binary cleanup: %v\n", err)
			}

			if !keepVMStore {
				implodeHostDatastore(w)
			}

			implodeSystemUnits(w)

			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "✓ Nexus removed from this machine (best-effort).")
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&keepVMStore, "keep-vm-store", false, "Do not delete contents of /data/nexus (Linux VM images)")
	return cmd
}

func stopAllLocalDaemons(wOut, wErr io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(wOut, "warning: home dir: %v\n", err)
		home = ""
	}

	prodState := filepath.Join(resolveStateBase(home), "nexus")
	prodSock := filepath.Join(prodState, "nexusd.sock")
	n := StopLocalDaemon(wOut, wErr, prodSock, 7777)
	fmt.Fprintf(wOut, "stopped %d process(es) for prod daemon (socket %s)\n", n, prodSock)

	devSock := filepath.Join(home, ".local", "state-dev", "nexus", "nexusd.sock")
	nd := StopLocalDaemon(wOut, wErr, devSock, 7778)
	fmt.Fprintf(wOut, "stopped %d process(es) for dev daemon (socket %s)\n", nd, devSock)
}

func resolveStateBase(home string) string {
	if sh := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); sh != "" {
		return start.ExpandTilde(sh)
	}
	if home != "" {
		return filepath.Join(home, ".local", "state")
	}
	return ".local/state"
}

func implodeUserTrees(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	xdgConfig, errCfg := os.UserConfigDir()
	if errCfg != nil {
		xdgConfig = filepath.Join(home, ".config")
	}

	xdgState := os.Getenv("XDG_STATE_HOME")
	if strings.TrimSpace(xdgState) == "" {
		xdgState = filepath.Join(home, ".local", "state")
	} else {
		xdgState = start.ExpandTilde(xdgState)
	}
	xdgData := os.Getenv("XDG_DATA_HOME")
	if strings.TrimSpace(xdgData) == "" {
		xdgData = filepath.Join(home, ".local", "share")
	} else {
		xdgData = start.ExpandTilde(xdgData)
	}

	dirs := []string{
		filepath.Join(xdgState, "nexus"),
		filepath.Join(xdgData, "nexus"),
		filepath.Join(home, ".local", "state-dev", "nexus"),
		filepath.Join(home, ".local", "share-dev", "nexus"),
		filepath.Join(xdgConfig, "nexus"),
		filepath.Join(home, ".nexus"),
	}

	matches, _ := filepath.Glob(filepath.Join(home, ".local", "state-nexus-*", "nexus"))
	dirs = append(dirs, matches...)
	matches2, _ := filepath.Glob(filepath.Join(home, ".local", "share-nexus-*"))
	dirs = append(dirs, matches2...)

	seen := make(map[string]struct{})
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if dir == "." || strings.TrimSpace(dir) == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
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

func unlinkCommonSockets(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	stateBase := resolveStateBase(home)

	paths := []string{
		filepath.Join(stateBase, "nexus", "nexusd.sock"),
		filepath.Join(stateBase, "nexus", "pty-host.sock"),
		filepath.Join(home, ".local", "state-dev", "nexus", "nexusd.sock"),
		filepath.Join(home, ".local", "state-dev", "nexus", "pty-host.sock"),
	}
	for _, p := range paths {
		if fi, err := os.Lstat(p); err != nil || fi.Mode()&os.ModeSocket == 0 {
			continue
		}
		fmt.Fprintf(w, "==> Removing socket %s\n", p)
		_ = os.Remove(p)
	}
	return nil
}

func implodeRemoveBins(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	bins := []string{
		filepath.Join(home, ".local", "bin", "nexus"),
		filepath.Join(home, ".local", "bin", "pty-host"),
		filepath.Join(home, ".local", "bin", "passt"),
		"/usr/local/bin/nexus",
		"/usr/local/bin/pty-host",
	}
	for _, bin := range bins {
		fi, statErr := os.Lstat(bin)
		if statErr != nil || fi.IsDir() {
			continue
		}
		fmt.Fprintf(w, "==> Removing %s\n", bin)
		_ = os.Remove(bin)
	}
	return nil
}
