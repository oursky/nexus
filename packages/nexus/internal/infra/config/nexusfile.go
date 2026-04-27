package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// NexusfileConfig is a lightweight project config loaded from Nexusfile.
// It is intentionally small and user-facing.
type NexusfileConfig struct {
	Schema   string               `json:"$schema,omitempty" toml:"$schema"`
	VM       NexusfileVMSection   `json:"vm,omitempty" toml:"vm"`
	Manifest NexusfileManifest    `json:"manifest,omitempty" toml:"manifest"`
	Dev      NexusfileDevSection  `json:"dev,omitempty" toml:"dev"`
}

type NexusfileVMSection struct {
	// Profile controls in-guest tool installation behavior.
	// Supported values: "minimal", "default" (or omitted).
	Profile string `json:"profile,omitempty" toml:"profile"`
	// Image is reserved for future prebuilt image/channel resolution.
	Image string `json:"image,omitempty" toml:"image"`
}

// NexusfileManifest is an extension point for base-layer cache invalidation.
// Any change to this section forces a rebuild of the cached base image.
type NexusfileManifest struct {
	// Empty for now — presence alone affects cache invalidation.
	// Future fields: base image version, toolchain pins, etc.
}

// NexusfileDevSection mirrors smolvm's [dev] table for future init/volume support.
type NexusfileDevSection struct {
	// Init commands run once when the workspace base layer is built.
	Init []string `json:"init,omitempty" toml:"init"`
	// Volumes declares host→guest mount mappings.
	Volumes []string `json:"volumes,omitempty" toml:"volumes"`
}

// LoadNexusfile loads <projectRoot>/Nexusfile. Missing file is not an error.
func LoadNexusfile(projectRoot string) (NexusfileConfig, bool, error) {
	path := filepath.Join(projectRoot, "Nexusfile")
	return loadNexusfileAtPath(path)
}

// BaseNexusfilePath returns the path to the user-scoped base Nexusfile.
func BaseNexusfilePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "nexus", "Nexusfile")
}

// LoadBaseNexusfile loads ~/.config/nexus/Nexusfile. Missing file is not an error.
func LoadBaseNexusfile() (NexusfileConfig, bool, error) {
	path := BaseNexusfilePath()
	if path == "" {
		return NexusfileConfig{}, false, nil
	}
	return loadNexusfileAtPath(path)
}

func loadNexusfileAtPath(path string) (NexusfileConfig, bool, error) {
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

// ComputeManifestHash returns a short hex hash that changes whenever the
// base Nexusfile, project Nexusfile, or bake stamp version changes.
// It is used as part of the per-repo base image cache key.
func ComputeManifestHash(basePath, projectPath, bakeVersion string) string {
	h := sha256.New()
	if data, err := os.ReadFile(basePath); err == nil && len(data) > 0 {
		h.Write(data)
	}
	h.Write([]byte{0})
	if data, err := os.ReadFile(projectPath); err == nil && len(data) > 0 {
		h.Write(data)
	}
	h.Write([]byte{0})
	h.Write([]byte(bakeVersion))
	return fmt.Sprintf("%x", h.Sum(nil)[:8])
}
