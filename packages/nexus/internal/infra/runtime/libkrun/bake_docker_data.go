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
