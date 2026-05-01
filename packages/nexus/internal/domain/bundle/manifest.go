package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
)

const SchemaVersion = "1"
const BundleVersion = "1.0.0"

// BundleManifest is the top-level manifest stored in manifest.json inside a .nxbundle.
type BundleManifest struct {
	SchemaVersion   string            `json:"schemaVersion"`
	BundleVersion   string            `json:"bundleVersion"`
	CreatedAt       string            `json:"createdAt"`
	Source          SourceMetadata    `json:"source"`
	Compatibility   CompatibilityMeta `json:"compatibility"`
	WorkspaceIntent WorkspaceIntent   `json:"workspaceIntent"`
	Payload         PayloadIndex      `json:"payload"`
	Integrity       IntegrityMetadata `json:"integrity"`
}

// SourceMetadata describes the origin of the exported workspace.
type SourceMetadata struct {
	WorkspaceName string `json:"workspaceName"`
	Ref           string `json:"ref"`
	ProjectHint   string `json:"projectHint"`
}

// CompatibilityMeta declares the environments the bundle is compatible with.
type CompatibilityMeta struct {
	Arch     []string `json:"arch"`
	Backend  []string `json:"backend"`
	OsFamily []string `json:"osFamily"`
}

// WorkspaceIntent captures the lifecycle commands associated with the workspace.
type WorkspaceIntent struct {
	Bake     []string `json:"bake"`
	Init     []string `json:"init"`
	Up       []string `json:"up"`
	Down     []string `json:"down"`
	InitMode string   `json:"initMode"`
}

// PayloadEntry references a single file/segment inside the bundle archive.
type PayloadEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// PayloadIndex is the collection of all payload entries in the bundle.
type PayloadIndex struct {
	Entries []PayloadEntry `json:"entries"`
}

// IntegrityMetadata holds the digest of the manifest itself.
type IntegrityMetadata struct {
	ManifestDigest string `json:"manifestDigest"`
	Algorithm      string `json:"algorithm"`
}

// WorkspaceIntentFromNexusfile reads a Nexusfile at the given path and maps
// bake/init/up/down fields into a WorkspaceIntent. Missing file is not an error.
func WorkspaceIntentFromNexusfile(path string) (WorkspaceIntent, error) {
	// If path points to a directory, look for Nexusfile inside it.
	// If path ends with "Nexusfile", use its directory.
	dir := path
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		dir = filepath.Dir(path)
	}

	cfg, found, err := config.LoadNexusfile(dir)
	if err != nil {
		return WorkspaceIntent{}, fmt.Errorf("bundle: reading Nexusfile: %w", err)
	}
	if !found {
		return WorkspaceIntent{}, nil
	}

	intent := WorkspaceIntent{
		Bake: cfg.Dev.Bake,
		Init: cfg.Dev.Init,
		Up:   cfg.Dev.Up,
		Down: cfg.Dev.Down,
	}
	return intent, nil
}

// MarshalManifest serialises m to indented JSON.
func MarshalManifest(m BundleManifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("bundle: marshal manifest: %w", err)
	}
	return data, nil
}

// ParseManifest deserialises manifest JSON bytes.
func ParseManifest(data []byte) (BundleManifest, error) {
	var m BundleManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return BundleManifest{}, fmt.Errorf("bundle: parse manifest: %w", err)
	}
	return m, nil
}

// ValidateManifest returns an InvalidBundle error if required fields are missing.
func ValidateManifest(m BundleManifest) error {
	if m.SchemaVersion == "" {
		return &InvalidBundle{Reason: "missing schemaVersion"}
	}
	if m.BundleVersion == "" {
		return &InvalidBundle{Reason: "missing bundleVersion"}
	}
	if m.CreatedAt == "" {
		return &InvalidBundle{Reason: "missing createdAt"}
	}
	if m.Source.WorkspaceName == "" {
		return &InvalidBundle{Reason: "missing source.workspaceName"}
	}
	if m.Integrity.ManifestDigest == "" {
		return &InvalidBundle{Reason: "missing integrity.manifestDigest"}
	}
	if m.Integrity.Algorithm == "" {
		return &InvalidBundle{Reason: "missing integrity.algorithm"}
	}
	return nil
}

// — error types ——————————————————————————————————————————

// InvalidBundle is returned when a bundle cannot be parsed or is structurally incomplete.
type InvalidBundle struct {
	Reason string
}

func (e *InvalidBundle) Error() string {
	return fmt.Sprintf("bundle: invalid bundle: %s", e.Reason)
}

// IncompatibleHost is returned when the host arch/OS does not match the bundle requirements.
type IncompatibleHost struct {
	Want []string
	Got  string
}

func (e *IncompatibleHost) Error() string {
	return fmt.Sprintf("bundle: incompatible host: bundle requires arch %v, got %s", e.Want, e.Got)
}

// IntegrityViolation is returned when the manifest digest does not match.
type IntegrityViolation struct {
	Expected string
	Got      string
}

func (e *IntegrityViolation) Error() string {
	return fmt.Sprintf("bundle: integrity violation: expected %s, got %s", e.Expected, e.Got)
}

// BootstrapFailure wraps errors that occur during workspace bootstrap from a bundle.
type BootstrapFailure struct {
	Cause error
}

func (e *BootstrapFailure) Error() string {
	return fmt.Sprintf("bundle: bootstrap failure: %v", e.Cause)
}

func (e *BootstrapFailure) Unwrap() error { return e.Cause }

// IntentMappingError is returned when workspace intent fields cannot be resolved.
type IntentMappingError struct {
	Cause error
}

func (e *IntentMappingError) Error() string {
	return fmt.Sprintf("bundle: intent mapping error: %v", e.Cause)
}

func (e *IntentMappingError) Unwrap() error { return e.Cause }
