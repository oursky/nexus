package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/auth/tokenstore"
)

// Profile stores connection details for a remote daemon.
// Token is intentionally omitted from JSON — it is stored separately in the
// OS keychain (macOS Keychain / Linux SecretService / headless file store).
type Profile struct {
	Name              string `json:"name"`
	Host              string `json:"host"`                        // SSH target, e.g. "newman@linuxbox"
	Port              int    `json:"port"`                        // Remote daemon port, e.g. 7777
	Token             string `json:"-"`                           // never written to JSON; use Load/SaveToken
	SSHPort           int    `json:"sshPort,omitempty"`           // SSH port override (default: 22)
	SSHIdentityFile   string `json:"sshIdentityFile,omitempty"`   // SSH identity file (private key path)
	LocalPort         int    `json:"localPort,omitempty"`         // forward tunnel local port
	ReverseTunnelPort int    `json:"reverseTunnelPort,omitempty"` // reverse tunnel port
}

// ProfilesDir returns the directory where profiles are stored.
// Uses $XDG_CONFIG_HOME/nexus/profiles/ or ~/.config/nexus/profiles/
func ProfilesDir() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "nexus", "profiles"), nil
}

// DefaultProfilePath returns the path to the default profile file.
func DefaultProfilePath() (string, error) {
	dir, err := ProfilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "default.json"), nil
}

// SaveDefault writes the default profile to disk and stores the token
// separately in the OS keychain.
func SaveDefault(p *Profile) error {
	dir, err := ProfilesDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	path := filepath.Join(dir, "default.json")
	// Token is json:"-" so it is never marshalled into the file.
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write profile: %w", err)
	}
	if p.Token != "" {
		if err := tokenstore.SaveProfileToken(p.Token); err != nil {
			return fmt.Errorf("save profile token to keychain: %w", err)
		}
	}
	return nil
}

// LoadDefault reads the default profile and populates Token from the OS keychain.
// If NEXUS_DAEMON_SSH_HOST is set, a synthetic profile is constructed from env vars
// (NEXUS_DAEMON_SSH_HOST, NEXUS_DAEMON_SSH_PORT, NEXUS_DAEMON_SSH_IDENTITY, NEXUS_DAEMON_TOKEN)
// instead of reading from disk. This allows the Mac app to inject connection details
// when running the CLI binary from a sandboxed environment.
func LoadDefault() (*Profile, error) {
	if host := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_HOST")); host != "" {
		p := &Profile{
			Name:  "env",
			Host:  host,
			Port:  7777,
			Token: strings.TrimSpace(os.Getenv("NEXUS_DAEMON_TOKEN")),
		}
		if portStr := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_PORT")); portStr != "" {
			if n, err := strconv.Atoi(portStr); err == nil {
				p.SSHPort = n
			}
		}
		if id := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_IDENTITY")); id != "" {
			p.SSHIdentityFile = id
		}
		return p, nil
	}

	path, err := DefaultProfilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no daemon profile configured. Run: nexus daemon connect <host>")
		}
		return nil, fmt.Errorf("read profile: %w", err)
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	tok, found, err := tokenstore.LoadProfileToken()
	if err != nil {
		return nil, fmt.Errorf("load profile token from keychain: %w", err)
	}
	if found {
		p.Token = tok
	}
	return &p, nil
}

// DeleteDefault removes the default profile file and its keychain token.
func DeleteDefault() error {
	path, err := DefaultProfilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove profile: %w", err)
	}
	_ = tokenstore.DeleteProfileToken()
	return nil
}
