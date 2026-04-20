package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Profile stores connection details for a remote daemon.
type Profile struct {
	Name    string `json:"name"`
	Host    string `json:"host"`              // SSH target, e.g. "newman@linuxbox"
	Port    int    `json:"port"`              // Remote daemon port, e.g. 7777
	Token   string `json:"token,omitempty"`   // Bearer token
	SSHPort int    `json:"sshPort,omitempty"` // SSH port override (default: 22)
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

// LoadDefault reads the default profile from disk.
// Returns an error if the profile does not exist.
func LoadDefault() (*Profile, error) {
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
	return &p, nil
}

// SaveDefault writes the default profile to disk.
func SaveDefault(p *Profile) error {
	dir, err := ProfilesDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	path := filepath.Join(dir, "default.json")
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write profile: %w", err)
	}
	return nil
}

// DeleteDefault removes the default profile file.
func DeleteDefault() error {
	path, err := DefaultProfilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove profile: %w", err)
	}
	return nil
}
