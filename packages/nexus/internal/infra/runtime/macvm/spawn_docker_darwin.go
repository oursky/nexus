//go:build darwin

package macvm

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

const dockerDiskSizeBytes = 50 * 1024 * 1024 * 1024 // sparse 50 GiB — matches Linux bake layout

func resolveMkfsExt4() string {
	if p, err := exec.LookPath("mkfs.ext4"); err == nil {
		return p
	}
	for _, p := range []string{
		"/opt/homebrew/opt/e2fsprogs/sbin/mkfs.ext4",
		"/usr/local/opt/e2fsprogs/sbin/mkfs.ext4",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ensureDockerDataExt4 creates a sparse docker-data ext4 at path when mkfs.ext4 is
// available. If mkfs.ext4 is missing (common on stock macOS), it returns skip=true so
// the VM can boot without a dedicated docker disk (guest agent skips docker mount).
func ensureDockerDataExt4(path string) (skip bool, err error) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		return false, nil
	}
	mkfs := resolveMkfsExt4()
	if mkfs == "" {
		_ = os.Remove(path)
		log.Printf("macvm: mkfs.ext4 not found; omitting docker-data disk (install e2fsprogs to enable /var/lib/docker in the guest)")
		return true, nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return false, fmt.Errorf("create docker-data image: %w", err)
	}
	if err := f.Truncate(dockerDiskSizeBytes); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("truncate docker-data image: %w", err)
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	out, err := exec.CommandContext(context.Background(), mkfs, "-F", "-L", "nexus-docker", path).CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("mkfs.ext4 docker-data: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return false, nil
}
