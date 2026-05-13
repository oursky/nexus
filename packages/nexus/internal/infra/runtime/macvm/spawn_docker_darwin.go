//go:build darwin

package macvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const dockerDiskSizeBytes = 50 * 1024 * 1024 * 1024 // sparse 50 GiB — matches Linux bake layout

func ensureDockerDataExt4(path string) error {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create docker-data image: %w", err)
	}
	if err := f.Truncate(dockerDiskSizeBytes); err != nil {
		_ = f.Close()
		return fmt.Errorf("truncate docker-data image: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	out, err := exec.CommandContext(context.Background(), "mkfs.ext4", "-F", "-L", "nexus-docker", path).CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("mkfs.ext4 docker-data: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
