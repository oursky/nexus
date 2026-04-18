package tokenstore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultPath returns the OS-appropriate fallback file path for the daemon token.
func DefaultPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "nexus", "daemon-token")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "nexus", "daemon-token")
}

// probe selects the best available Store for the current platform/environment.
// On Linux with a D-Bus session bus (and the nodbus tag absent), SecretServiceStore
// is preferred. All other cases fall back to FileStore at DefaultPath().
func probe() Store {
	if runtime.GOOS == "linux" && secretServiceAvailable() {
		ss, err := NewSecretServiceStore()
		if err == nil {
			return ss
		}
	}
	return NewFileStore(DefaultPath())
}

// LoadOrGenerate loads the daemon token from the best available store.
// If no token exists, it generates a cryptographically random 32-byte hex token,
// persists it, and returns it. The same store is used for both operations.
func LoadOrGenerate() (string, error) {
	s := probe()
	token, found, err := s.Load()
	if err != nil {
		return "", fmt.Errorf("tokenstore: load: %w", err)
	}
	if found && token != "" {
		return token, nil
	}

	token, err = generateToken()
	if err != nil {
		return "", fmt.Errorf("tokenstore: generate: %w", err)
	}
	if err := s.Save(token); err != nil {
		return "", fmt.Errorf("tokenstore: save: %w", err)
	}
	return token, nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
