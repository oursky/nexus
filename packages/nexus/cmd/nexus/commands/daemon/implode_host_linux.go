//go:build linux

package daemon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func implodeHostDatastore(w io.Writer) {
	const root = "/data/nexus"
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintf(w, "warning: list %s: %v\n", root, err)
		return
	}
	for _, ent := range entries {
		child := filepath.Join(root, ent.Name())
		info, ierr := os.Lstat(child)
		if ierr != nil {
			continue
		}
		if strings.HasPrefix(strings.ToLower(ent.Name()), ".nfs") || ent.Name() == "lost+found" {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		fmt.Fprintf(w, "==> Removing %s\n", child)
		if err := os.RemoveAll(child); err != nil {
			fmt.Fprintf(w, "warning: remove %s: %v\n", child, err)
		}
	}
}

func implodeSystemUnits(w io.Writer) {
	out, err := exec.Command("systemctl", "--user", "list-unit-files", "--type=service", "--no-pager", "--no-legend").CombinedOutput()
	if err != nil {
		return
	}
	touched := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		unit := fields[0]
		if unit == "-" {
			continue
		}
		if !strings.Contains(strings.ToLower(unit), "nexus") {
			continue
		}
		_ = exec.Command("systemctl", "--user", "disable", "--now", unit).Run()
		fmt.Fprintf(w, "==> disabled user systemd unit %s\n", unit)
		touched = true
	}
	if !touched {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range ents {
		if ent.IsDir() {
			continue
		}
		name := strings.ToLower(ent.Name())
		if strings.Contains(name, "nexus") {
			fp := filepath.Join(dir, ent.Name())
			fmt.Fprintf(w, "==> Removing %s\n", fp)
			_ = os.Remove(fp)
		}
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
}
