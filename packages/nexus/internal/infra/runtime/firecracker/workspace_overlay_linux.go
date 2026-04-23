//go:build linux

package firecracker

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// workspaceOverlaySizeBytes is the size of the per-workspace writable overlay
// image. It is a sparse file so actual disk usage grows only with writes.
const workspaceOverlaySizeBytes = 16 * 1024 * 1024 * 1024 // 16 GiB sparse

// repoBaseKey returns a short stable identifier derived from the canonical
// repo root path. Used as a directory name under BasesDir.
func repoBaseKey(repoRoot string) string {
	h := sha256.Sum256([]byte(filepath.Clean(repoRoot)))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars, collision-resistant enough
}

// EnsureBaseImage returns the path to a cached read-only base ext4 image for
// the given repo root, building it if it does not yet exist.
// Building is O(repo size) and should be called from the async start goroutine.
func EnsureBaseImage(repoRoot, basesDir string) (string, error) {
	key := repoBaseKey(repoRoot)
	dir := filepath.Join(basesDir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("base image dir: %w", err)
	}
	basePath := filepath.Join(dir, "base.ext4")

	if _, err := os.Stat(basePath); err == nil {
		log.Printf("[firecracker] base image cache hit: %s", basePath)
		return basePath, nil
	}

	log.Printf("[firecracker] building base image for %s → %s", repoRoot, basePath)
	if err := buildBaseImage(repoRoot, basePath); err != nil {
		_ = os.Remove(basePath)
		return "", fmt.Errorf("build base image: %w", err)
	}
	log.Printf("[firecracker] base image ready: %s", basePath)
	return basePath, nil
}

// buildBaseImage creates an ext4 image populated with the repo files.
// This is the slow, one-time operation (O(repo size)).
func buildBaseImage(repoRoot, imagePath string) error {
	if strings.TrimSpace(repoRoot) == "" {
		return fmt.Errorf("repoRoot is required")
	}
	projectSizeBytes, err := directorySizeBytes(repoRoot)
	if err != nil {
		return fmt.Errorf("compute project size: %w", err)
	}

	fd, err := os.OpenFile(imagePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create base image: %w", err)
	}
	if err := fd.Truncate(workspaceImageSizeBytes(projectSizeBytes)); err != nil {
		_ = fd.Close()
		return fmt.Errorf("truncate base image: %w", err)
	}
	if err := fd.Close(); err != nil {
		return fmt.Errorf("close base image: %w", err)
	}

	cmd := exec.Command("mkfs.ext4", "-F", "-d", repoRoot, imagePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 base image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// createWorkspaceOverlay creates a sparse ext4 image for the per-workspace
// writable overlay layer. Because it is sparse, creation takes only seconds
// regardless of repo size. Actual disk usage grows only as the user writes.
func createWorkspaceOverlay(overlayPath string) error {
	fd, err := os.OpenFile(overlayPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create overlay image: %w", err)
	}
	if err := fd.Truncate(workspaceOverlaySizeBytes); err != nil {
		_ = fd.Close()
		return fmt.Errorf("truncate overlay image: %w", err)
	}
	if err := fd.Close(); err != nil {
		return fmt.Errorf("close overlay image: %w", err)
	}

	// Format as ext4 — fast since there are no data blocks to copy.
	cmd := exec.Command("mkfs.ext4", "-F", overlayPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(overlayPath)
		return fmt.Errorf("mkfs.ext4 overlay: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
