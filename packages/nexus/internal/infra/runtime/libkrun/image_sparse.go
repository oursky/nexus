//go:build linux

package libkrun

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func createSparseFile(path string, size int64) error {
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := fd.Truncate(size); err != nil {
		_ = fd.Close()
		return fmt.Errorf("truncate %s: %w", path, err)
	}
	return fd.Close()
}

func directorySizeBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func workspaceImageSizeBytes(projectSizeBytes int64) int64 {
	const (
		miB        = int64(1024 * 1024)
		giB        = 1024 * miB
		minSize    = 2 * giB
		overhead   = 2 * giB
		maxInitial = 20 * giB
	)
	if raw := strings.TrimSpace(os.Getenv("NEXUS_WORKSPACE_IMAGE_MIN_MIB")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			minVal := n * miB
			target := projectSizeBytes*2 + minVal/2
			if target < minVal {
				target = minVal
			}
			if target > maxInitial {
				target = maxInitial
			}
			if rem := target % miB; rem != 0 {
				target += miB - rem
			}
			return target
		}
	}
	target := projectSizeBytes*2 + overhead
	if target < minSize {
		target = minSize
	}
	if target > maxInitial {
		target = maxInitial
	}
	if rem := target % miB; rem != 0 {
		target += miB - rem
	}
	return target
}

// resizeImage grows a workspace image to newSizeBytes using truncate + resize2fs.
func resizeImage(imagePath string, newSizeBytes int64) error {
	if err := os.Truncate(imagePath, newSizeBytes); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	out, err := exec.Command("resize2fs", imagePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("resize2fs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
