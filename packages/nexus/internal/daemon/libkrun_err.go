package daemon

import "errors"

// ErrLibkrunNotBootstrapped is returned by buildLibkrunDriver on macOS when
// libkrun dylibs are not present under the Nexus share lib directory (bootstrap
// skipped or not yet run). The daemon refuses to start until this is resolved.
var ErrLibkrunNotBootstrapped = errors.New(
	"libkrun.dylib not found — VM workspaces unavailable (run `nexus daemon start` to bootstrap)",
)
