package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// NexusfileConfig is a lightweight project config loaded from Nexusfile.
// It is intentionally small and user-facing.
type NexusfileConfig struct {
	Schema string             `json:"$schema,omitempty"`
	VM     NexusfileVMSection `json:"vm,omitempty"`
}

type NexusfileVMSection struct {
	// Profile controls in-guest tool installation behavior.
	// Supported values: "minimal", "dev".
	Profile string `json:"profile,omitempty"`
	// Image is reserved for future prebuilt image/channel resolution.
	Image string `json:"image,omitempty"`
}

// LoadNexusfile loads <projectRoot>/Nexusfile. Missing file is not an error.
func LoadNexusfile(projectRoot string) (NexusfileConfig, bool, error) {
	path := filepath.Join(projectRoot, "Nexusfile")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NexusfileConfig{}, false, nil
		}
		return NexusfileConfig{}, false, err
	}

	var cfg NexusfileConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return NexusfileConfig{}, false, err
	}
	cfg.VM.Profile = strings.ToLower(strings.TrimSpace(cfg.VM.Profile))
	return cfg, true, nil
}
