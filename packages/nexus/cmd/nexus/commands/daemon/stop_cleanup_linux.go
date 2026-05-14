//go:build linux

package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
)

func cleanupDaemonResidue() {
	// Ensure child helpers are torn down even if daemon shutdown raced.
	for _, name := range []string{"nexus-libkrun-vm", "nexus-guest-agent", "passt", "pasta", "pty-host"} {
		_ = exec.Command("pkill", "-x", name).Run()
	}

	workRoots := []string{
		"/data/nexus/default",
		"/data/nexus/libkrun-vms", // legacy workdir-root default
		filepath.Join(defaultStateDataDir(), "default"),
		filepath.Join(defaultStateDataDir(), "libkrun-vms"),
	}
	for _, root := range workRoots {
		cleanupWorkRoot(root)
	}

	// Drop stale bake temp directories and sockets from interrupted runs.
	for _, glob := range []string{
		"/tmp/nexus-rootfs-bake-*",
		"/tmp/nexus-baked-rootfs-*",
	} {
		matches, _ := filepath.Glob(glob)
		for _, p := range matches {
			_ = os.RemoveAll(p)
		}
	}
}

func defaultStateDataDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); strings.TrimSpace(xdg) != "" {
		return filepath.Join(start.ExpandTilde(xdg), "nexus")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "/var/lib/nexus"
	}
	return filepath.Join(home, ".local", "state", "nexus")
}

func cleanupWorkRoot(root string) {
	if strings.TrimSpace(root) == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(root, ent.Name())
		killByPIDFile(filepath.Join(dir, "libkrun.pid"))
		killByPIDFile(filepath.Join(dir, "passt.pid"))
		vsocks, _ := filepath.Glob(filepath.Join(dir, "vsock_*.sock"))
		for _, s := range vsocks {
			_ = os.Remove(s)
		}
	}
}

func killByPIDFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid := 0
	for _, ch := range strings.TrimSpace(string(data)) {
		if ch < '0' || ch > '9' {
			pid = 0
			break
		}
		pid = pid*10 + int(ch-'0')
	}
	if pid > 1 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	_ = os.Remove(path)
}
