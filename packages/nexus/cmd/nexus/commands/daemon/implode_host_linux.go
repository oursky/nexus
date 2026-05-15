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

const (
	nexusHostDataRoot        = "/data/nexus"
	nexusHostLoopBackingFile = "/var/lib/nexus-xfs-backing.img"
)

// linuxTeardownNexusDataVolumeScript unmounts install.sh loop mounts (nested first),
// removes the sparse backing image, and deletes /data/nexus for a clean reinstall.
//
// Uses fuser -km on mountpoints when available so implode succeeds after VMs / shells
// hold files under /data/nexus (lsof-based daemon stop alone is not always enough).
const linuxTeardownNexusDataVolumeScript = `
set -uo pipefail
DATA_ROOT='` + nexusHostDataRoot + `'
BACKING='` + nexusHostLoopBackingFile + `'

kill_mount_users() {
  local mp="$1"
  command -v fuser >/dev/null 2>&1 || return 0
  fuser -km "$mp" 2>/dev/null || true
}

try_umount_mp() {
  local mp="$1"
  command -v mountpoint >/dev/null 2>&1 || return 0
  mountpoint -q "$mp" || return 0
  local attempt
  for attempt in 1 2 3 4; do
    sync 2>/dev/null || true
    kill_mount_users "$mp"
    sleep 1
    if umount "$mp" 2>/dev/null; then
      return 0
    fi
  done
  echo "nexus-implode-teardown: still busy: $mp" >&2
  command -v fuser >/dev/null 2>&1 && fuser -vm "$mp" >&2 || true
  command -v lsof >/dev/null 2>&1 && lsof +f -- "$mp" 2>/dev/null | head -n 30 >&2 || true
  return 1
}

fail=0
for mp in "$DATA_ROOT/default" "$DATA_ROOT"; do
  try_umount_mp "$mp" || fail=1
done

if command -v mountpoint >/dev/null 2>&1; then
  if mountpoint -q "$DATA_ROOT/default" 2>/dev/null || mountpoint -q "$DATA_ROOT" 2>/dev/null; then
    fail=1
  fi
fi

if [ "$fail" -ne 0 ]; then
  echo "nexus-implode-teardown: could not unmount $DATA_ROOT - exit shells whose working directory is under this path, stop VMs, retry" >&2
  echo "nexus-implode-teardown: manual: sudo fuser -km $DATA_ROOT/default $DATA_ROOT; sudo umount $DATA_ROOT/default $DATA_ROOT" >&2
  exit 1
fi

rm -f "$BACKING"
rm -rf "$DATA_ROOT"
`

func implodeHostDatastore(w io.Writer) {
	if teardownNexusHostDataVolume(w) {
		return
	}
	if fi, err := os.Stat(nexusHostDataRoot); err == nil && fi.IsDir() {
		fmt.Fprintf(w, "warning: privileged /data/nexus teardown failed or was denied; trying non-root cleanup only\n")
		implodeHostDatastoreUnprivileged(w)
	}
}

func teardownNexusHostDataVolume(w io.Writer) bool {
	var cmd *exec.Cmd
	if os.Geteuid() == 0 {
		cmd = exec.Command("bash", "-ceu", linuxTeardownNexusDataVolumeScript)
	} else {
		cmd = exec.Command("sudo", "bash", "-ceu", linuxTeardownNexusDataVolumeScript)
	}
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		fmt.Fprint(w, string(out))
	}
	if err != nil {
		fmt.Fprintf(w, "warning: teardown script: %v\n", err)
		return false
	}
	fmt.Fprintf(w, "==> Removed %s (unmounted if needed) and %s when present\n", nexusHostDataRoot, nexusHostLoopBackingFile)
	return true
}

// implodeHostDatastoreUnprivileged deletes children without sudo (legacy behavior).
func implodeHostDatastoreUnprivileged(w io.Writer) {
	fi, err := os.Stat(nexusHostDataRoot)
	if err != nil || !fi.IsDir() {
		return
	}
	entries, err := os.ReadDir(nexusHostDataRoot)
	if err != nil {
		fmt.Fprintf(w, "warning: list %s: %v\n", nexusHostDataRoot, err)
		return
	}
	for _, ent := range entries {
		child := filepath.Join(nexusHostDataRoot, ent.Name())
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
