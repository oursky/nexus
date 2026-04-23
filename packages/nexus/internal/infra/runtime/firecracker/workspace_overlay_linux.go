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
	"syscall"
)

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

// ReflinkAvailable reports whether dir's filesystem supports copy-on-write
// reflinks (XFS or btrfs). When true, CreateWorkspaceReflink can be used to
// create a per-workspace copy of the base image in O(1) time.
func ReflinkAvailable(dir string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return false
	}
	const (
		xfsMagic   = 0x58465342 // XFS_SUPER_MAGIC
		btrfsMagic = 0x9123683E // BTRFS_SUPER_MAGIC
	)
	return st.Type == xfsMagic || st.Type == int64(btrfsMagic)
}

// FilesystemType returns a human-readable name for the filesystem hosting dir.
func FilesystemType(dir string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return "unknown"
	}
	switch st.Type {
	case 0x58465342:
		return "xfs"
	case 0x9123683E:
		return "btrfs"
	case 0xEF53:
		return "ext4"
	case 0x6969, 0x6E667364:
		return "nfs"
	default:
		return fmt.Sprintf("0x%X", st.Type)
	}
}

// CreateWorkspaceReflink creates a per-workspace writable ext4 image as an
// O(1) copy-on-write clone of basePath. Both files must be on the same XFS or
// btrfs filesystem. Writes in the workspace clone do not affect the shared base.
func CreateWorkspaceReflink(basePath, workspacePath string) error {
	out, err := exec.Command("cp", "--reflink=always", basePath, workspacePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("reflink %s → %s: %w: %s", basePath, workspacePath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

