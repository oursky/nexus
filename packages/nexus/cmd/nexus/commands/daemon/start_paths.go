package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// defaultVMKernelPath returns the user-scoped kernel path under XDG_DATA_HOME.
func defaultVMKernelPath() string {
	return xdgVMAsset("vmlinux.bin")
}

// defaultVMRootfsPath returns the user-scoped rootfs path under XDG_DATA_HOME.
func defaultVMRootfsPath() string {
	return xdgVMAsset("rootfs.ext4")
}

// xdgVMAsset returns the path to a VM asset under ~/.local/share/nexus/vm/
// or the legacy /var/lib/nexus/ path if the legacy file still exists and the
// XDG file does not, providing seamless migration for existing setups.
func xdgVMAsset(name string) string {
	home, _ := os.UserHomeDir()
	xdgPath := filepath.Join(home, ".local", "share", "nexus", "vm", name)
	legacyPath := "/var/lib/nexus/" + name

	if _, err := os.Stat(xdgPath); err == nil {
		return xdgPath
	}
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath
	}
	return xdgPath
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/var/lib/nexus"
	}
	return filepath.Join(home, ".local", "state", "nexus")
}

// agentHashFile returns a user-writable path for the agent SHA-256 cache.
func agentHashFile() string {
	return filepath.Join(defaultDataDir(), "rootfs-agent.sha256")
}

func preferredLibkrunStorageRoot() (string, bool) {
	const root = "/data/nexus"
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return root, true
}

func promoteVMAssetToDir(srcPath, destDir string) (string, error) {
	src := strings.TrimSpace(srcPath)
	if src == "" {
		return "", fmt.Errorf("source path is empty")
	}
	if strings.TrimSpace(destDir) == "" {
		return src, nil
	}
	base := filepath.Base(src)
	dest := filepath.Join(destDir, base)
	if filepath.Clean(src) == filepath.Clean(dest) {
		return dest, nil
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("stat source %s: %w", src, err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	if dstInfo, err := os.Stat(dest); err == nil {
		// Reuse existing promoted asset if it matches source freshness and size.
		if dstInfo.Size() == srcInfo.Size() && !srcInfo.ModTime().After(dstInfo.ModTime()) {
			return dest, nil
		}
	}

	tmp := dest + ".tmp"
	_ = os.Remove(tmp)
	out, cpErr := exec.Command("cp", "--reflink=auto", "--sparse=auto", src, tmp).CombinedOutput()
	if cpErr != nil {
		return "", fmt.Errorf("cp %s -> %s: %w: %s", src, tmp, cpErr, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("rename %s -> %s: %w", tmp, dest, err)
	}
	return dest, nil
}
