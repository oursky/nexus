//go:build darwin

package macvm

import (
	"os"
	"path/filepath"
)

func applyConfigDefaults(cfg ManagerConfig) ManagerConfig {
	cacheDir := nexusCacheDir()
	if cfg.LibDir == "" {
		cfg.LibDir = filepath.Join(nexusShareDir(), "lib")
	}
	if cfg.VMWorkDir == "" {
		cfg.VMWorkDir = filepath.Join(cacheDir, "macvm-workspaces")
	}
	if cfg.RootFSCachePath == "" {
		cfg.RootFSCachePath = filepath.Join(cacheDir, "vm", "rootfs.ext4")
	}
	return cfg
}

func nexusCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "nexus")
}

func nexusShareDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus")
}
