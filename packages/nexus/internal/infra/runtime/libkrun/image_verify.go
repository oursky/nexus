//go:build linux

package libkrun

import (
	"os/exec"
	"strings"
)

func rootfsHasBakeStamp(rootfsPath string) bool {
	const stampInsideRootfs = "/var/lib/nexus-tools-base-v15"
	if strings.TrimSpace(rootfsPath) == "" {
		return false
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return false
	}
	out, err := exec.Command("debugfs", "-R", "stat "+stampInsideRootfs, rootfsPath).CombinedOutput()
	joined := strings.ToLower(string(out))
	// debugfs exits 0 even when the file is not found; treat "not found" as
	// missing regardless of exit code.
	if strings.Contains(joined, "file not found") || strings.Contains(joined, "not found by ext2_lookup") {
		return false
	}
	if err == nil {
		return true
	}
	// debugfs can emit warnings to stderr while still producing usable output.
	// Treat explicit inode matches as success.
	return strings.Contains(joined, "inode:")
}
