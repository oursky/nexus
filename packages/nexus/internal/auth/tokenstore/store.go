package tokenstore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultLinuxFilePath returns the fallback file path for headless Linux daemons
// (no D-Bus session available — e.g. a remote server without a desktop session).
func DefaultLinuxFilePath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "nexus", "daemon-token")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "nexus", "daemon-token")
}

// probe selects the appropriate Store for the current platform:
//   - macOS  → KeychainStore (system Keychain via `security` CLI)
//   - Linux with D-Bus session → SecretServiceStore (GNOME Keyring / KWallet)
//   - Linux headless (no D-Bus) → FileStore at DefaultLinuxFilePath (0600)
//
// FileStore is intentionally never used on macOS; Keychain is always available.
func probe() Store {
	switch runtime.GOOS {
	case "darwin":
		return NewKeychainStore()
	case "linux":
		if secretServiceAvailable() {
			if ss, err := NewSecretServiceStore(); err == nil {
				return ss
			}
		}
		// Headless server: no D-Bus session. File store is acceptable here
		// (same security model as SSH host keys — 0600, owner-only).
		return NewFileStore(DefaultLinuxFilePath())
	default:
		return NewFileStore(DefaultLinuxFilePath())
	}
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

// profileTokenStore returns the Store used for the client-side profile token
// (distinct from the daemon's own token store).
func profileTokenStore() Store {
	return probe()
}

// SaveProfileToken persists the client-side bearer token (fetched from the
// remote daemon during `nexus daemon connect`) to the OS keychain.
func SaveProfileToken(token string) error {
	return profileTokenStore().Save(token)
}

// LoadProfileToken retrieves the client-side bearer token from the OS keychain.
func LoadProfileToken() (token string, found bool, err error) {
	return profileTokenStore().Load()
}

// Deleter is an optional interface stores may implement to support explicit deletion.
type Deleter interface {
	Delete() error
}

// DeleteProfileToken removes the client-side bearer token from the OS keychain.
// Best-effort: if the store does not implement Deleter the call is a no-op.
func DeleteProfileToken() error {
	if d, ok := profileTokenStore().(Deleter); ok {
		return d.Delete()
	}
	return nil
}
