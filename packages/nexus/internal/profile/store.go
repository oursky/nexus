package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	RemoteHost  string `json:"remoteHost"`
	RemotePort  int    `json:"remotePort"`
	SSHPort     int    `json:"sshPort"`
	SSHIdentity string `json:"sshIdentity"`
	LocalPort   int    `json:"localPort"`
	Token       string `json:"token"`
	IsDefault   bool   `json:"isDefault"`
	CreatedAt   string `json:"createdAt"`
}

type Store struct {
	path string
}

func NewStore() *Store {
	return &Store{path: defaultPath()}
}

func defaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "profiles.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/etc/nexus/profiles.json"
	}
	return filepath.Join(home, ".config", "nexus", "profiles.json")
}

func (s *Store) Load() ([]Profile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Profile{}, nil
		}
		return nil, fmt.Errorf("profile store load: %w", err)
	}
	var profiles []Profile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, fmt.Errorf("profile store parse: %w", err)
	}
	return profiles, nil
}

func (s *Store) Save(profiles []Profile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("profile store mkdir: %w", err)
	}
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return fmt.Errorf("profile store marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("profile store write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("profile store rename: %w", err)
	}
	return nil
}

func (s *Store) AddOrUpdate(p Profile) error {
	profiles, err := s.Load()
	if err != nil {
		return err
	}
	for i, existing := range profiles {
		if existing.ID == p.ID {
			profiles[i] = p
			return s.Save(profiles)
		}
	}
	profiles = append(profiles, p)
	return s.Save(profiles)
}

func (s *Store) SetDefault(id string) error {
	profiles, err := s.Load()
	if err != nil {
		return err
	}
	found := false
	for i := range profiles {
		if profiles[i].ID == id {
			profiles[i].IsDefault = true
			found = true
		} else {
			profiles[i].IsDefault = false
		}
	}
	if !found {
		return fmt.Errorf("profile %q not found", id)
	}
	return s.Save(profiles)
}

func (s *Store) Default() (*Profile, error) {
	profiles, err := s.Load()
	if err != nil {
		return nil, err
	}
	for i := range profiles {
		if profiles[i].IsDefault {
			return &profiles[i], nil
		}
	}
	if len(profiles) > 0 {
		return &profiles[0], nil
	}
	return nil, fmt.Errorf("no profiles found")
}

func (s *Store) ByName(name string) (*Profile, error) {
	profiles, err := s.Load()
	if err != nil {
		return nil, err
	}
	for i := range profiles {
		if profiles[i].Name == name {
			return &profiles[i], nil
		}
	}
	return nil, fmt.Errorf("profile %q not found", name)
}
