//go:build linux

package libkrun

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func bakeTimeout() time.Duration {
	const defaultTimeout = 12 * time.Minute
	raw := strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_BAKE_TIMEOUT"))
	if raw == "" {
		return defaultTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("[libkrun] rootfs bake: invalid NEXUS_LIBKRUN_BAKE_TIMEOUT=%q; using %s", raw, defaultTimeout)
		return defaultTimeout
	}
	return d
}

func bakeMaxAttempts() int {
	const defaultAttempts = 1
	raw := strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS"))
	if raw == "" {
		return defaultAttempts
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		log.Printf("[libkrun] rootfs bake: invalid NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=%q; using %d", raw, defaultAttempts)
		return defaultAttempts
	}
	return n
}

// BakeRootfsIfNeeded checks whether the base rootfs already has developer
// tools pre-installed (stamp present). If not, it boots a temporary VM in
// "bake mode": the agent installs packages + npm tools synchronously then
// powers off the VM. The resulting rootfs becomes the new base so every
// subsequent workspace clone starts with tools already present.
//
// stampDir is the directory to store the host-side bake stamp
// (e.g. ~/.local/state/nexus). Non-fatal: logs a warning and returns nil
// on failure so daemon start is never blocked.
func BakeRootfsIfNeeded(ctx context.Context, cfg ManagerConfig, stampDir string) error {
	stampPath := filepath.Join(stampDir, "rootfs-baked-"+BakeStampVersion)
	if _, err := os.Stat(stampPath); err == nil {
		log.Printf("[libkrun] rootfs bake: already baked (stamp %s)", stampPath)
		return nil
	}

	if cfg.LibkrunVMBin == "" {
		return fmt.Errorf("LibkrunVMBin not set — cannot bake rootfs")
	}
	if cfg.RootFSBasePath == "" {
		return fmt.Errorf("RootFSBasePath not set — cannot bake rootfs")
	}
	if _, err := os.Stat(cfg.RootFSBasePath); err != nil {
		return fmt.Errorf("base rootfs not found at %s: %w", cfg.RootFSBasePath, err)
	}
	// If the host-side stamp is missing but the rootfs itself still has the
	// in-image tools stamp, restore the host stamp and skip the expensive bake.
	if rootfsHasBakeStamp(cfg.RootFSBasePath) {
		if err := os.MkdirAll(stampDir, 0o755); err == nil {
			_ = os.WriteFile(stampPath, []byte("baked\n"), 0o644)
		}
		log.Printf("[libkrun] rootfs bake: in-image stamp detected in %s; restored host stamp %s and skipped rebake", cfg.RootFSBasePath, stampPath)
		return nil
	}

	timeout := bakeTimeout()
	maxAttempts := bakeMaxAttempts()
	log.Printf("[libkrun] rootfs bake: pre-installing tools with timeout=%s max_attempts=%d", timeout, maxAttempts)

	var bakedRootfsPath string
	var bakeErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			log.Printf("[libkrun] rootfs bake: retrying bake (attempt %d/%d)...", attempt, maxAttempts)
		}
		cleanupStaleBakeArtifacts()
		bakedRootfsPath, bakeErr = runBakeVM(ctx, cfg)
		if bakeErr == nil {
			break
		}
		log.Printf("[libkrun] rootfs bake: attempt %d failed: %v", attempt, bakeErr)
		if attempt < maxAttempts {
			// Wait before retrying — network/DNS issues may be transient.
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
	}
	if bakeErr != nil {
		return fmt.Errorf("bake VM failed after %d attempt(s): %w", maxAttempts, bakeErr)
	}
	// Always remove the temp baked rootfs when we're done with it.
	defer os.Remove(bakedRootfsPath)

	// Atomically replace the base rootfs with the baked version.
	tmpDest := cfg.RootFSBasePath + ".bake-new"
	if err := copyFile(bakedRootfsPath, tmpDest); err != nil {
		return fmt.Errorf("copy baked rootfs: %w", err)
	}
	if err := os.Rename(tmpDest, cfg.RootFSBasePath); err != nil {
		_ = os.Remove(tmpDest)
		return fmt.Errorf("replace base rootfs: %w", err)
	}

	// Write bake stamp so subsequent daemon starts skip baking.
	if err := os.MkdirAll(stampDir, 0o755); err == nil {
		_ = os.WriteFile(stampPath, []byte("baked\n"), 0o644)
	}

	log.Printf("[libkrun] rootfs bake: complete — all future workspace clones will start with tools pre-installed")
	return nil
}
