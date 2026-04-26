package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// NexusfileConfig is a lightweight project config loaded from Nexusfile.
// It is intentionally small and user-facing.
type NexusfileConfig struct {
	Schema string             `json:"$schema,omitempty" toml:"$schema"`
	VM     NexusfileVMSection `json:"vm,omitempty" toml:"vm"`
}

type NexusfileVMSection struct {
	// Profile controls in-guest tool installation behavior.
	// Supported values: "minimal", "default" (or omitted).
	Profile string `json:"profile,omitempty" toml:"profile"`
	// Image is reserved for future prebuilt image/channel resolution.
	Image string `json:"image,omitempty" toml:"image"`
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
	if err := toml.Unmarshal(data, &cfg); err != nil {
		// Backward-compat: accept legacy JSON Nexusfile.
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if jerr := dec.Decode(&cfg); jerr != nil {
			return NexusfileConfig{}, false, err
		}
	}
	cfg.VM.Profile = strings.ToLower(strings.TrimSpace(cfg.VM.Profile))
	return cfg, true, nil
}
