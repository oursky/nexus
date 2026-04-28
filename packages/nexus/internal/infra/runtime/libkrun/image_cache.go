//go:build linux

package libkrun

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
		if !isValidExt4(basePath) {
			log.Printf("[libkrun] base image cache hit but ext4 magic invalid, removing corrupt file: %s", basePath)
			_ = os.Remove(basePath)
		} else {
			log.Printf("[libkrun] base image cache hit: %s", basePath)
			return basePath, nil
		}
	}

	// Acquire per-repo lock to prevent concurrent builds.
	muVal, _ := baseImageLocks.LoadOrStore(key, &sync.Mutex{})
	mu := muVal.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// Remove any stale .tmp from a previous crashed build.
	_ = os.Remove(basePath + ".tmp")

	// Double-check after acquiring lock.
	if _, err := os.Stat(basePath); err == nil {
		if !isValidExt4(basePath) {
			log.Printf("[libkrun] base image cache hit (after lock) but ext4 magic invalid, removing corrupt file: %s", basePath)
			_ = os.Remove(basePath)
		} else {
			log.Printf("[libkrun] base image cache hit (after lock): %s", basePath)
			return basePath, nil
		}
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
