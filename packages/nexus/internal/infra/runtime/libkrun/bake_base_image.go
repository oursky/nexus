//go:build linux

package libkrun

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

func buildBaseImage(repoRoot, imagePath string) error {
	if strings.TrimSpace(repoRoot) == "" {
		return fmt.Errorf("repoRoot is required")
	}
	size, err := directorySizeBytes(repoRoot)
	if err != nil {
		return fmt.Errorf("compute project size: %w", err)
	}
	tmpPath := imagePath + ".tmp"
	if err := createSparseFile(tmpPath, workspaceImageSizeBytes(size)); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", "-d", repoRoot, tmpPath).CombinedOutput()
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

		cmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", "-d", repoRoot, tmpPath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = fmt.Errorf("mkfs.ext4 base image: %w: %s", err, strings.TrimSpace(string(out)))
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
