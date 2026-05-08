package sync

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// SSHManager manages SSH keys for cross-host sync.
// It generates and stores an ed25519 key pair used by mutagen to sync
// between the daemon and remote clients (e.g., Mac app).
type SSHManager struct {
	stateDir string // e.g., ~/.local/state/nexus
}

// NewSSHManager creates a new SSHManager backed by the given state directory.
func NewSSHManager(stateDir string) *SSHManager {
	return &SSHManager{stateDir: stateDir}
}

// KeyPath returns the path to the private key.
func (m *SSHManager) KeyPath() string {
	return filepath.Join(m.stateDir, "sync", "ssh", "id_ed25519")
}

// PublicKeyPath returns the path to the public key.
func (m *SSHManager) PublicKeyPath() string {
	return m.KeyPath() + ".pub"
}

// EnsureKeys generates an SSH key pair if one doesn't exist.
func (m *SSHManager) EnsureKeys() error {
	if _, err := os.Stat(m.KeyPath()); err == nil {
		return nil // already exists
	}
	if err := os.MkdirAll(filepath.Dir(m.KeyPath()), 0700); err != nil {
		return fmt.Errorf("ssh: failed to create key directory: %w", err)
	}

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("ssh: failed to generate key: %w", err)
	}

	// Write private key in PEM format
	privBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return fmt.Errorf("ssh: failed to marshal private key: %w", err)
	}
	privPEM := &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}
	privFile, err := os.OpenFile(m.KeyPath(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("ssh: failed to create private key file: %w", err)
	}
	defer privFile.Close()
	if err := pem.Encode(privFile, privPEM); err != nil {
		return fmt.Errorf("ssh: failed to write private key: %w", err)
	}

	// Generate public key
	pubKey, err := ssh.NewPublicKey(privKey.Public())
	if err != nil {
		return fmt.Errorf("ssh: failed to create public key: %w", err)
	}
	pubBytes := ssh.MarshalAuthorizedKey(pubKey)
	if err := os.WriteFile(m.PublicKeyPath(), pubBytes, 0644); err != nil {
		return fmt.Errorf("ssh: failed to write public key: %w", err)
	}

	return nil
}

// PublicKey returns the public key in authorized_keys format.
func (m *SSHManager) PublicKey() (string, error) {
	if err := m.EnsureKeys(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(m.PublicKeyPath())
	if err != nil {
		return "", fmt.Errorf("ssh: failed to read public key: %w", err)
	}
	return string(data), nil
}

// BuildSSHURL creates an SSH URL for syncing to a remote host.
// Format: user@host:path
func (m *SSHManager) BuildSSHURL(user, host, path string) string {
	return fmt.Sprintf("%s@%s:%s", user, host, path)
}

// IsRemotePath checks if a path is a remote SSH path.
func IsRemotePath(path string) bool {
	return strings.Contains(path, "@") || strings.HasPrefix(path, "ssh://")
}

// DetectTransport determines if a sync should use local or SSH transport.
// Returns "ssh" if the path looks remote, "local" otherwise.
func DetectTransport(path string) string {
	if IsRemotePath(path) {
		return "ssh"
	}
	return "local"
}