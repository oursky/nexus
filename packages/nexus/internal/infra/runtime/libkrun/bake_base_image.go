//go:build linux

package libkrun

import (
	"context"
	"fmt"
	"log"
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
	if err := createSparseFile(imagePath, workspaceImageSizeBytes(size)); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", "-d", repoRoot, imagePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 base image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
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
