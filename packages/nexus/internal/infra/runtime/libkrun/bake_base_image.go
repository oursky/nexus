//go:build linux

package libkrun

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func buildBaseImage(repoRoot, imagePath string) error {
	if strings.TrimSpace(repoRoot) == "" {
		return fmt.Errorf("repoRoot is required")
	}
	sourceRoot, cleanup, err := prepareBaseImageSource(context.Background(), repoRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	size, err := directorySizeBytes(repoRoot)
	if err != nil {
		return fmt.Errorf("compute project size: %w", err)
	}
	tmpPath := imagePath + ".tmp"
	if err := createSparseFile(tmpPath, workspaceImageSizeBytes(size)); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", "-d", sourceRoot, tmpPath).CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("mkfs.ext4 base image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmpPath, imagePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("promote base image: %w", err)
	}
	return nil
}

// buildBaseImageWithContext builds a base image with context support and timeout.
// Retries transient failures up to 3 times with exponential backoff.
// The image is written to imagePath+".tmp" and atomically renamed to imagePath on success.
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
	sourceRoot, cleanup, err := prepareBaseImageSource(ctx, repoRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	size, err := directorySizeBytes(repoRoot)
	if err != nil {
		return fmt.Errorf("compute project size: %w", err)
	}

	tmpPath := imagePath + ".tmp"
	if err := createSparseFile(tmpPath, workspaceImageSizeBytes(size)); err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(attempt-1) * 500 * time.Millisecond
			log.Printf("[libkrun] buildBaseImage retry attempt=%d backoff=%s: %s → %s", attempt, backoff, repoRoot, imagePath)
			select {
			case <-ctx.Done():
				_ = os.Remove(tmpPath)
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		cmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", "-d", sourceRoot, tmpPath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = annotateMKFSError(err, out, repoRoot, size)
	}

	if lastErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("buildBaseImage failed after 3 attempts: %w", lastErr)
	}

	// Validate with e2fsck if available.
	if fsck, err := exec.LookPath("e2fsck"); err == nil {
		cmd := exec.CommandContext(ctx, fsck, "-n", tmpPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[libkrun] e2fsck validation FAILED for %s: %v: %s", tmpPath, err, strings.TrimSpace(string(out)))
			_ = os.Remove(tmpPath)
			return fmt.Errorf("e2fsck validation failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		log.Printf("[libkrun] base image validated OK: %s", tmpPath)
	} else {
		log.Printf("[libkrun] e2fsck not found, skipping validation: %v", err)
	}

	if err := os.Rename(tmpPath, imagePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("promote base image: %w", err)
	}
	return nil
}

func annotateMKFSError(err error, out []byte, repoRoot string, repoSize int64) error {
	trimmed := strings.TrimSpace(string(out))
	if strings.Contains(trimmed, "No space left on device") {
		imageSize := workspaceImageSizeBytes(repoSize)
		return fmt.Errorf(
			"mkfs.ext4 base image: %w: %s (repo=%s size=%d bytes image=%d bytes); repository snapshot exceeds image capacity; trim large directories (for example .worktrees) or increase NEXUS_WORKSPACE_IMAGE_MIN_MIB",
			err,
			trimmed,
			repoRoot,
			repoSize,
			imageSize,
		)
	}
	return fmt.Errorf("mkfs.ext4 base image: %w: %s", err, trimmed)
}

func prepareBaseImageSource(ctx context.Context, repoRoot string) (string, func(), error) {
	gitPath := filepath.Join(repoRoot, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil || info.IsDir() {
		return repoRoot, func() {}, nil
	}

	worktreeGitDir, ok, err := parseGitdirFile(gitPath)
	if err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", gitPath, err)
	}
	if !ok {
		return repoRoot, func() {}, nil
	}
	if !filepath.IsAbs(worktreeGitDir) {
		worktreeGitDir = filepath.Clean(filepath.Join(repoRoot, worktreeGitDir))
	}

	stagingRoot, err := os.MkdirTemp("", "nexus-base-src-")
	if err != nil {
		return "", nil, fmt.Errorf("create staging root: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(stagingRoot) }

	if err := copyTreeWithContext(ctx, repoRoot, stagingRoot); err != nil {
		cleanup()
		return "", nil, err
	}

	commonGitDir := worktreeGitDir
	if raw, readErr := os.ReadFile(filepath.Join(worktreeGitDir, "commondir")); readErr == nil {
		rel := strings.TrimSpace(string(raw))
		if rel != "" {
			if filepath.IsAbs(rel) {
				commonGitDir = rel
			} else {
				commonGitDir = filepath.Clean(filepath.Join(worktreeGitDir, rel))
			}
		}
	}

	stagedGitDir := filepath.Join(stagingRoot, ".git")
	if err := os.Remove(stagedGitDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("remove staged .git file: %w", err)
	}
	if err := copyPathWithContext(ctx, commonGitDir, stagedGitDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("copy common git dir: %w", err)
	}
	_ = os.RemoveAll(filepath.Join(stagedGitDir, "worktrees"))

	if err := copyOptionalFile(filepath.Join(worktreeGitDir, "HEAD"), filepath.Join(stagedGitDir, "HEAD")); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("copy worktree HEAD: %w", err)
	}
	if err := copyOptionalFile(filepath.Join(worktreeGitDir, "index"), filepath.Join(stagedGitDir, "index")); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("copy worktree index: %w", err)
	}
	if err := copyOptionalFile(filepath.Join(worktreeGitDir, "logs", "HEAD"), filepath.Join(stagedGitDir, "logs", "HEAD")); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("copy worktree logs/HEAD: %w", err)
	}

	log.Printf("[libkrun] materialized linked worktree git metadata for base image: source=%s gitdir=%s", repoRoot, worktreeGitDir)
	return stagingRoot, cleanup, nil
}

func parseGitdirFile(path string) (string, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", false, nil
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if gitDir == "" {
		return "", false, fmt.Errorf("empty gitdir value")
	}
	return gitDir, true, nil
}

func copyTreeWithContext(ctx context.Context, srcDir, dstDir string) error {
	cmd := exec.CommandContext(ctx, "cp", "-a", filepath.Join(srcDir, "."), dstDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy tree %s -> %s: %w: %s", srcDir, dstDir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyPathWithContext(ctx context.Context, srcPath, dstPath string) error {
	cmd := exec.CommandContext(ctx, "cp", "-a", srcPath, dstPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy path %s -> %s: %w: %s", srcPath, dstPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyOptionalFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if info, statErr := os.Stat(src); statErr == nil {
		perm = info.Mode().Perm()
	}
	return os.WriteFile(dst, data, perm)
}

// isValidExt4 does a fast superblock magic check on path.
// Returns false if the file cannot be read or the magic is wrong.
func isValidExt4(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var buf [2]byte
	if _, err := f.ReadAt(buf[:], 1080); err != nil {
		return false
	}
	return buf[0] == 0x53 && buf[1] == 0xef
}
