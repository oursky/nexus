//go:build darwin

package macvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type spawnConfig struct {
	workspaceID   string
	workDir       string
	rootFSPath    string
	workspacePath string
	configDir     string
	libDir        string
}

// spawnVM boots a libkrun microVM for the given workspace.
// It requires libkrun.dylib and libkrunfw.dylib in cfg.libDir.
// On first use, stage them via: task stage:libkrun-macos
func spawnVM(ctx context.Context, cfg spawnConfig) (*vmInstance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	libkrunPath := filepath.Join(cfg.libDir, "libkrun.dylib")
	if _, err := os.Stat(libkrunPath); os.IsNotExist(err) {
		return nil, fmt.Errorf(
			"libkrun.dylib not found at %s; stage it first with: task stage:libkrun-macos",
			cfg.libDir,
		)
	}

	return nil, fmt.Errorf(
		"macvm: VM boot not yet implemented; workspace=%s rootfs=%s workspace=%s",
		cfg.workspaceID, cfg.rootFSPath, cfg.workspacePath,
	)
}
