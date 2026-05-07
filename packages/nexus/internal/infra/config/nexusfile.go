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
	Schema   string                  `json:"$schema,omitempty" toml:"$schema"`
	VM       NexusfileVMSection      `json:"vm,omitempty" toml:"vm"`
	Manifest NexusfileManifest       `json:"manifest,omitempty" toml:"manifest"`
	Dev      NexusfileDevSection     `json:"dev,omitempty" toml:"dev"`
	Sandbox  NexusfileSandboxSection `json:"sandbox,omitempty" toml:"sandbox"`
}

// NexusfileSandboxSection configures process-sandbox isolation for the workspace.
type NexusfileSandboxSection struct {
	// Relaxed disables network and PID isolation so that processes spawned
	// inside the workspace (e.g. a nexus daemon for self-hosted development)
	// can bind to host-visible ports and survive past the terminal session.
	// Only enable this for trusted internal repos.
	Relaxed bool `json:"relaxed,omitempty" toml:"relaxed"`
}

type NexusfileVMSection struct {
	// Profile controls in-guest tool installation behavior.
	// Supported values: "minimal", "default" (or omitted).
	Profile string `json:"profile,omitempty" toml:"profile"`
	// Image is reserved for future prebuilt image/channel resolution.
	Image string `json:"image,omitempty" toml:"image"`
	// CPUs sets the number of virtual CPUs for the bundle VM.
	// Defaults to 2 when omitted or zero.
	CPUs uint8 `json:"cpus,omitempty" toml:"cpus"`
	// MemMiB sets the VM memory size in MiB.
	// Defaults to 2048 when omitted or zero.
	MemMiB uint32 `json:"mem,omitempty" toml:"mem"`
}

// NexusfileManifest is an extension point for base-layer cache invalidation.
// Any change to this section forces a rebuild of the cached base image.
type NexusfileManifest struct {
	// Empty for now — presence alone affects cache invalidation.
	// Future fields: base image version, toolchain pins, etc.
}

// NexusfileDevPort declares a port to forward for the workspace.
type NexusfileDevPort struct {
	// Port is the host port to tunnel to.
	Port int `json:"port" toml:"port"`
	// Service is an optional human-readable name (e.g. "web", "api").
	Service string `json:"service,omitempty" toml:"service"`
	// Protocol is "tcp" or "udp". Defaults to "tcp".
	Protocol string `json:"protocol,omitempty" toml:"protocol"`
}

// NexusfileDevSection mirrors smolvm's [dev] table for lifecycle command support.
type NexusfileDevSection struct {
	// Init commands run once when the workspace base layer is built.
	Init []string `json:"init,omitempty" toml:"init"`
	// Bake commands run when the shared base image is baked.
	Bake []string `json:"bake,omitempty" toml:"bake"`
	// Up commands start the workspace services (e.g. docker compose up).
	Up []string `json:"up,omitempty" toml:"up"`
	// Down commands stop workspace services (e.g. docker compose down).
	Down []string `json:"down,omitempty" toml:"down"`
	// Volumes declares host→guest mount mappings.
	Volumes []string `json:"volumes,omitempty" toml:"volumes"`
	// Port is the primary local port for auto-forwarding and health checks.
	Port int `json:"port,omitempty" toml:"port"`
	// Ports declares additional ports to forward, overriding compose auto-discovery.
	Ports []NexusfileDevPort `json:"ports,omitempty" toml:"ports"`
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
