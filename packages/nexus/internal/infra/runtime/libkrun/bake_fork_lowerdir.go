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

// buildForkLowerdirWithContext builds a flat ext4 image for use as the
// read-only lowerdir of a fork workspace's hybrid overlayfs mount.
//
// The parent workspace uses:
//   - lowerdir = virtiofs share (host project dir, read-only)
//   - upperdir = workspace.ext4:/upper (parent's writes)
//
// The snapshot captured by ForkWorkspaceImage is a copy of the parent's
// workspace.ext4, which contains an overlay directory structure:
//
//	upper/   ← parent's writes (files the parent created/modified in /workspace)
//	work/    ← overlay internals (opaque whiteouts etc.)
//
// A fork child cannot use virtiofs (it uses a separate project root worktree
// on the host), so it relies solely on workspace-base.ext4 as its lowerdir.
// For overlayfs to work correctly, this lowerdir must be a flat ext4 image
// whose root contains the actual workspace files — not the overlay internal
// directory structure.
//
// This function merges:
//  1. The project root source tree (original repo files visible to the parent)
//  2. The parent's overlay writes extracted from snapshot upper/
//
// Parent writes take precedence over original files (they overwrite them in
// the staging directory before mkfs.ext4 is run).
//
// Special case: when the snapshot was produced from EnsureBaseImage (parent
// was never started), the snapshot already contains flat repo content with no
// upper/ directory. In that case the snapshot is used as-is (copy path).
func buildForkLowerdirWithContext(ctx context.Context, snapshotPath, projectRoot, outputPath string) error {
	start := time.Now()
	log.Printf("[libkrun] buildForkLowerdir: snapshot=%s projectRoot=%s → %s", snapshotPath, projectRoot, outputPath)
	defer func() {
		log.Printf("[libkrun] buildForkLowerdir done: %s (%s)", outputPath, time.Since(start).Round(time.Millisecond))
	}()

	// 1. Prepare a staging directory populated with repo files.
	//    prepareBaseImageSource handles worktree .git file resolution.
	staging, cleanupStaging, err := prepareBaseImageSource(ctx, projectRoot)
	if err != nil {
		return fmt.Errorf("prepare base image source: %w", err)
	}
	defer cleanupStaging()

	// 2. Mount the snapshot read-only and look for an overlay upper/ directory.
	//    The snapshot (parent's workspace.ext4) was an overlayfs upper device,
	//    so its contents are:
	//      upper/  ← parent's mutations
	//      work/   ← overlay work dir (ignored here)
	//    If upper/ is present, merge its contents over the staging tree so that
	//    parent writes shadow the original repo files.
	tmpMount, err := os.MkdirTemp("", "nexus-fork-snap-*")
	if err != nil {
		return fmt.Errorf("create tmp mount point: %w", err)
	}
	defer os.RemoveAll(tmpMount)

	mounted := false
	mountCmd := exec.CommandContext(ctx, "mount", "-o", "loop,ro,user_xattr", snapshotPath, tmpMount)
	if out, mountErr := mountCmd.CombinedOutput(); mountErr != nil {
		log.Printf("[libkrun] buildForkLowerdir: mount snapshot failed (%v: %s) – will build from project root only",
			mountErr, strings.TrimSpace(string(out)))
	} else {
		mounted = true
	}

	if mounted {
		defer func() {
			if out, err := exec.Command("umount", tmpMount).CombinedOutput(); err != nil {
				log.Printf("[libkrun] buildForkLowerdir: umount %s: %v: %s", tmpMount, err, strings.TrimSpace(string(out)))
			}
		}()

		upperDir := filepath.Join(tmpMount, "upper")
		if info, statErr := os.Stat(upperDir); statErr == nil && info.IsDir() {
			// Merge parent's overlay writes over staging (parent wins on conflicts).
			log.Printf("[libkrun] buildForkLowerdir: merging parent overlay upper/ into staging")
			if err := copyTreeWithContext(ctx, upperDir, staging); err != nil {
				return fmt.Errorf("merge parent overlay writes into staging: %w", err)
			}
		} else {
			// No upper/ directory → snapshot is a flat base image (parent was
			// never started). Nothing extra to merge; staging already has repo files.
			log.Printf("[libkrun] buildForkLowerdir: snapshot has no upper/ (flat base image) – using repo files only")
		}
	}

	// 3. Build a flat ext4 image from the merged staging directory.
	size, err := directorySizeBytes(staging)
	if err != nil {
		return fmt.Errorf("compute staging size: %w", err)
	}

	tmpPath := outputPath + ".tmp"
	if err := createSparseFile(tmpPath, workspaceImageSizeBytes(size)); err != nil {
		return err
	}

	mkfsOut, mkfsErr := exec.CommandContext(ctx, "mkfs.ext4", "-F", "-d", staging, tmpPath).CombinedOutput()
	if mkfsErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("mkfs.ext4 fork lowerdir: %w: %s", mkfsErr, strings.TrimSpace(string(mkfsOut)))
	}

	if err := os.Rename(tmpPath, outputPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("promote fork lowerdir: %w", err)
	}
	return nil
}
