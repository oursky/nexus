//go:build linux

package net

import (
	"os"
	"path/filepath"
)

// passtAssetData is populated by arch-specific embed files.

func extractEmbeddedPasstIfNeeded() error {
	if len(passtAssetData) == 0 {
		return nil
	}
	cacheBin := cachePasstPath()
	if _, err := os.Stat(cacheBin); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cacheBin), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cacheBin, passtAssetData, 0o755); err != nil {
		return err
	}
	return nil
}
