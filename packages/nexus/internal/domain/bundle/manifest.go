package bundle

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
)

// WorkspaceIntent captures the lifecycle commands and VM resource configuration
// associated with a workspace. It is read from the Nexusfile by the daemon and
// passed to the exporter, which converts it into BundleMeta.
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
