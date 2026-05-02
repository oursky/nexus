package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
)

const SchemaVersion = "2"
const BundleVersion = "2.0.0"

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
	// Runtime describes the VM/container configuration (SchemaVersion "2" only).
	Runtime *RuntimeConfig `json:"runtime,omitempty"`
	// Assets is the inventory of all files inside the bundle (SchemaVersion "2" only).
	Assets *AssetInventory `json:"assets,omitempty"`
}

// RuntimeConfig describes the VM or container runtime configuration for the bundle.
type RuntimeConfig struct {
	Mode   string `json:"mode"`   // "vm"
	Image  string `json:"image"`  // OCI image ref
	Digest string `json:"digest"` // sha256:...
	CPUs   uint8  `json:"cpus"`
	MemMiB uint32 `json:"memMiB"`
}

// AssetInventory is the inventory of all files packed inside an NXPACK bundle.
type AssetInventory struct {
	Libraries   []AssetEntry `json:"libraries"`
	AgentRootfs *AssetEntry  `json:"agentRootfs,omitempty"`
	Layers      []LayerEntry `json:"layers"`
	Workspace   *AssetEntry  `json:"workspace,omitempty"`
}

// AssetEntry describes a single file inside the bundle assets blob.
type AssetEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// LayerEntry describes an OCI layer packed inside the bundle assets blob.
type LayerEntry struct {
	Digest string `json:"digest"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
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

// WorkspaceIntent captures the lifecycle commands and VM resource configuration
// associated with the workspace.
type WorkspaceIntent struct {
	Bake     []string `json:"bake"`
	Init     []string `json:"init"`
	Up       []string `json:"up"`
	Down     []string `json:"down"`
	InitMode string   `json:"initMode"`
	// CPUs sets the number of virtual CPUs for the bundle VM.
	// Zero means "use exporter default".
	CPUs uint8 `json:"cpus,omitempty"`
	// MemMiB sets the VM memory size in MiB.
	// Zero means "use exporter default".
	MemMiB uint32 `json:"memMiB,omitempty"`
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
// bake/init/up/down and vm resource fields into a WorkspaceIntent.
// Missing file is not an error.
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
		Bake:   cfg.Dev.Bake,
		Init:   cfg.Dev.Init,
		Up:     cfg.Dev.Up,
		Down:   cfg.Dev.Down,
		CPUs:   cfg.VM.CPUs,
		MemMiB: cfg.VM.MemMiB,
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
