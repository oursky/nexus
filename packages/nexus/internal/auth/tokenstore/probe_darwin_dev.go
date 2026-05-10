//go:build darwin && dev

package tokenstore

import (
	"os"
	"path/filepath"
)

func probe() Store {
	// Dev builds on macOS use file-based token storage instead of Keychain.
	// This enables headless/CI environments where Keychain access is unavailable.
	// Prod builds (without the `dev` tag) always use KeychainStore.
	return NewFileStore(devTokenPath())
}

func devTokenPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "nexus-dev", "daemon-token")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "nexus-dev", "daemon-token")
}
