//go:build linux

//nolint:unused
package libkrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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
	return createWorkspaceOverlayWithContext(context.Background(), overlayPath)
}

func createWorkspaceOverlayWithContext(ctx context.Context, overlayPath string) error {
	if err := createSparseFile(overlayPath, workspaceOverlaySizeBytes); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "mkfs.ext4", "-F", overlayPath).CombinedOutput()
	if err != nil {
		_ = os.Remove(overlayPath)
		return fmt.Errorf("mkfs.ext4 overlay: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// createBakeWorkspaceImage creates a minimal empty ext4 image for the bake
// VM's /workspace mount. The bake agent doesn't use /workspace but the VM
// spec requires a block device for /dev/vdb.
func createBakeWorkspaceImage(path string) error {
	const bakeworkspaceSize = 512 * 1024 * 1024 // 512 MiB sparse
	if err := createSparseFile(path, bakeworkspaceSize); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", path).CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("mkfs.ext4 bake workspace: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
