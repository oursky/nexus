package libkrun

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const workspaceOverlaySizeBytes = 16 * 1024 * 1024 * 1024 // 16 GiB sparse

func repoBaseKey(repoRoot string) string {
	h := sha256.Sum256([]byte(filepath.Clean(repoRoot)))
	return fmt.Sprintf("%x", h[:8])
}

// EnsureBaseImage returns or builds a cached read-only base ext4 for the repo.
func EnsureBaseImage(repoRoot, basesDir string) (string, error) {
	key := repoBaseKey(repoRoot)
	dir := filepath.Join(basesDir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("base image dir: %w", err)
	}
	basePath := filepath.Join(dir, "base.ext4")
	if _, err := os.Stat(basePath); err == nil {
		log.Printf("[libkrun] base image cache hit: %s", basePath)
		return basePath, nil
	}
	log.Printf("[libkrun] building base image %s → %s", repoRoot, basePath)
	if err := buildBaseImage(repoRoot, basePath); err != nil {
		_ = os.Remove(basePath)
		return "", fmt.Errorf("build base image: %w", err)
	}
	return basePath, nil
}

func buildBaseImage(repoRoot, imagePath string) error {
	if strings.TrimSpace(repoRoot) == "" {
		return fmt.Errorf("repoRoot is required")
	}
	size, err := directorySizeBytes(repoRoot)
	if err != nil {
		return fmt.Errorf("compute project size: %w", err)
	}
	if err := createSparseFile(imagePath, workspaceImageSizeBytes(size)); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", "-d", repoRoot, imagePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 base image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func createWorkspaceImage(projectRoot, imagePath string) error {
	if strings.TrimSpace(projectRoot) == "" {
		return fmt.Errorf("project root is required")
	}
	size, err := directorySizeBytes(projectRoot)
	if err != nil {
		return fmt.Errorf("compute project size: %w", err)
	}
	if err := createSparseFile(imagePath, workspaceImageSizeBytes(size)); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", "-d", projectRoot, imagePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 workspace image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func createWorkspaceOverlay(overlayPath string) error {
	if err := createSparseFile(overlayPath, workspaceOverlaySizeBytes); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", overlayPath).CombinedOutput()
	if err != nil {
		_ = os.Remove(overlayPath)
		return fmt.Errorf("mkfs.ext4 overlay: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

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

// copyFile copies src→dst with CoW and sparse-file support.
func copyFile(src, dst string) error {
	out, err := exec.Command("cp", "--reflink=auto", "--sparse=always", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp %s → %s: %w: %s", src, dst, err, strings.TrimSpace(string(out)))
	}
	return nil
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
