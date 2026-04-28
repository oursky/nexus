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

		// Require reflink (O(1) CoW on XFS/btrfs). cp --reflink=always returns 0
		// even when reflink fails (falls back to regular copy on coreutils ≥9), so
		// we inspect stderr to detect the failure.
		cmd := exec.CommandContext(ctx, "cp", "--reflink=always", "--sparse=auto", src, dst)
		out, err := cmd.CombinedOutput()
		outStr := strings.TrimSpace(string(out))
		if err != nil {
			lastErr = fmt.Errorf("cp %s → %s: %w: %s", src, dst, err, outStr)
			continue
		}
		if strings.Contains(outStr, "failed to clone") {
			lastErr = fmt.Errorf("reflink clone failed for %s → %s: %s", src, dst, outStr)
			continue
		}
		log.Printf("[libkrun] copyFile reflink clone enabled: %s → %s", src, dst)
		return nil
	}
	return fmt.Errorf("copyFile failed after 3 attempts: %w", lastErr)
}
