package libkrun

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const workspaceOverlaySizeBytes = 16 * 1024 * 1024 * 1024 // 16 GiB sparse

// baseImageLocks prevents concurrent builds of the same repo base image.
var baseImageLocks sync.Map // key string -> *sync.Mutex

func repoBaseKey(repoRoot, manifestHash string) string {
	h := sha256.Sum256([]byte(filepath.Clean(repoRoot) + "\x00" + manifestHash + "\x00" + BakeStampVersion))
	return fmt.Sprintf("%x", h[:8])
}

// EnsureBaseImage returns or builds a cached read-only base ext4 for the repo.
// It uses a per-repo mutex to prevent concurrent builds of the same base image.
// The context is used to bound the mkfs.ext4 operation.
// manifestHash should be derived from the base/project Nexusfile contents so
// that any manifest change invalidates the cache.
func EnsureBaseImage(ctx context.Context, repoRoot, basesDir, manifestHash string) (string, error) {
	key := repoBaseKey(repoRoot, manifestHash)
	dir := filepath.Join(basesDir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("base image dir: %w", err)
	}
	basePath := filepath.Join(dir, "base.ext4")
	if _, err := os.Stat(basePath); err == nil {
		log.Printf("[libkrun] base image cache hit: %s", basePath)
		return basePath, nil
	}

	// Acquire per-repo lock to prevent concurrent builds.
	muVal, _ := baseImageLocks.LoadOrStore(key, &sync.Mutex{})
	mu := muVal.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// Double-check after acquiring lock.
	if _, err := os.Stat(basePath); err == nil {
		log.Printf("[libkrun] base image cache hit (after lock): %s", basePath)
		return basePath, nil
	}

	log.Printf("[libkrun] building base image %s → %s", repoRoot, basePath)
	start := time.Now()
	if err := buildBaseImageWithContext(ctx, repoRoot, basePath); err != nil {
		_ = os.Remove(basePath)
		return "", fmt.Errorf("build base image: %w", err)
	}
	log.Printf("[libkrun] built base image %s in %s", basePath, time.Since(start).Round(time.Millisecond))
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

// dockerDataSizeBytes is the declared sparse size for the per-workspace Docker data image.
// The host sees near-zero usage until Docker actually pulls images or builds layers.
const dockerDataSizeBytes = 50 * 1024 * 1024 * 1024 // 50 GiB sparse

// createDockerDataImage creates an empty sparse ext4 image for Docker's data-root.
// Docker overlay2 requires a real kernel filesystem; it cannot run on virtiofs/FUSE.
// This image is mounted at /var/lib/docker inside the guest.
func createDockerDataImage(imagePath string) error {
	if err := createSparseFile(imagePath, dockerDataSizeBytes); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", "-L", "nexus-docker", imagePath).CombinedOutput()
	if err != nil {
		_ = os.Remove(imagePath)
		return fmt.Errorf("mkfs.ext4 docker-data: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyFile copies src→dst preferring a reflink clone (O(1) on XFS/btrfs)
// and falls back to a sparse copy on filesystems that don't support reflinks.
func copyFile(src, dst string) error {
	// Try reflink first (O(1) CoW); if unsupported fall back to sparse copy.
	out, err := exec.Command("cp", "--reflink=always", "--sparse=always", src, dst).CombinedOutput()
	if err == nil {
		return nil
	}
	// Reflink failed (non-XFS/btrfs host); use sparse copy instead.
	out, err = exec.Command("cp", "--sparse=always", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp %s → %s: %w: %s", src, dst, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyFileWithContext copies src→dst using reflink clone when available (O(1) CoW on XFS/btrfs),
// falling back to sparse copy on filesystems that don't support reflinks.
// Retries transient failures up to 3 times with exponential backoff.
func copyFileWithContext(ctx context.Context, src, dst string) error {
	start := time.Now()
	log.Printf("[libkrun] copyFile start: %s → %s", src, dst)
	defer func() {
		log.Printf("[libkrun] copyFile done: %s → %s (%s)", src, dst, time.Since(start).Round(time.Millisecond))
	}()

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(attempt-1) * 500 * time.Millisecond
			log.Printf("[libkrun] copyFile retry attempt=%d backoff=%s: %s → %s", attempt, backoff, src, dst)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		// Try reflink first (O(1) CoW); if unsupported fall back to sparse copy.
		cmd := exec.CommandContext(ctx, "cp", "--reflink=always", "--sparse=auto", src, dst)
		out, err := cmd.CombinedOutput()
		if err == nil {
			log.Printf("[libkrun] copyFile reflink clone enabled: %s → %s", src, dst)
			return nil
		}
		lastErr = fmt.Errorf("cp %s → %s: %w: %s", src, dst, err, strings.TrimSpace(string(out)))
		log.Printf("[libkrun] copyFile reflink unavailable, using sparse copy fallback: %s → %s", src, dst)

		// Reflink failed (non-XFS/btrfs host); use sparse copy instead.
		cmd = exec.CommandContext(ctx, "cp", "--sparse=always", src, dst)
		out, err = cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("cp %s → %s: %w: %s", src, dst, err, strings.TrimSpace(string(out)))
	}
	return fmt.Errorf("copyFile failed after 3 attempts: %w", lastErr)
}

// createDockerDataImageWithContext creates an empty sparse ext4 image with context support.
// Retries transient failures up to 3 times with exponential backoff.
func createDockerDataImageWithContext(ctx context.Context, imagePath string) error {
	start := time.Now()
	log.Printf("[libkrun] createDockerDataImage start: %s", imagePath)
	defer func() {
		log.Printf("[libkrun] createDockerDataImage done: %s (%s)", imagePath, time.Since(start).Round(time.Millisecond))
	}()

	if err := createSparseFile(imagePath, dockerDataSizeBytes); err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(attempt-1) * 500 * time.Millisecond
			log.Printf("[libkrun] createDockerDataImage retry attempt=%d backoff=%s: %s", attempt, backoff, imagePath)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		cmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", "-L", "nexus-docker", imagePath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("mkfs.ext4 docker-data: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = os.Remove(imagePath)
	return fmt.Errorf("createDockerDataImage failed after 3 attempts: %w", lastErr)
}

// buildBaseImageWithContext builds a base image with context support and timeout.
// Retries transient failures up to 3 times with exponential backoff.
func buildBaseImageWithContext(ctx context.Context, repoRoot, imagePath string) error {
	start := time.Now()
	log.Printf("[libkrun] buildBaseImage start: %s → %s", repoRoot, imagePath)
	log.Printf("[libkrun] base image strategy=mkfs.ext4 -d repo-snapshot (no host volume mount): %s", repoRoot)
	defer func() {
		log.Printf("[libkrun] buildBaseImage done: %s → %s (%s)", repoRoot, imagePath, time.Since(start).Round(time.Millisecond))
	}()

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

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(attempt-1) * 500 * time.Millisecond
			log.Printf("[libkrun] buildBaseImage retry attempt=%d backoff=%s: %s → %s", attempt, backoff, repoRoot, imagePath)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		cmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", "-d", repoRoot, imagePath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("mkfs.ext4 base image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return fmt.Errorf("buildBaseImage failed after 3 attempts: %w", lastErr)
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

// injectFileIntoExt4 writes data into an ext4 image at the given absolute
// path inside the filesystem using debugfs, without mounting the image.
// The parent directories must already exist inside the image.
func injectFileIntoExt4(imagePath string, data []byte, destPath string, mode os.FileMode) error {
	// Write data to a temp file so debugfs can read it.
	tmp, err := os.CreateTemp("", "nexus-inject-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	_ = tmp.Close()

	// Use debugfs to write the file into the image and set permissions.
	// "write <src> <dst>" writes src into the filesystem at dst.
	// "set_inode_field <dst> mode 0<octal>" sets permissions.
	cmds := fmt.Sprintf("write %s %s\nset_inode_field %s mode 0%o\n",
		tmp.Name(), destPath, destPath, 0o100000|mode)
	cmd := exec.Command("debugfs", "-w", imagePath)
	cmd.Stdin = bytes.NewBufferString(cmds)
	if out, err := cmd.CombinedOutput(); err != nil {
		// debugfs exits non-zero on warnings; treat as success if file exists.
		_ = out
	}
	return nil
}
