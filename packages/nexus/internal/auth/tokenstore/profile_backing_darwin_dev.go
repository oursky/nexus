//go:build darwin && dev

package tokenstore

import (
	"os"
	"path/filepath"
)

// On macOS dev builds the daemon token lives in ~/.config/nexus-dev/daemon-token.
// Client profile tokens must use a separate file or `nexus daemon connect <remote>`
// overwrites the same path and breaks the local daemon's bearer auth.
func profileBackingStore() Store {
	return NewFileStore(devProfileBearerPath())
}

func devProfileBearerPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.Getenv("HOME"), ".config", "nexus-dev", "profile-bearer-token")
	}
	return filepath.Join(home, ".config", "nexus-dev", "profile-bearer-token")
}
