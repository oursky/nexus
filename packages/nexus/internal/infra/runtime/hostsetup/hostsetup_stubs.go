//go:build !darwin

package hostsetup

// EnsureMacOSRootfsTools is a no-op on non-Darwin platforms.
// The actual implementation is in hostsetup_darwin.go.
func EnsureMacOSRootfsTools() error { return nil }

// EnsureMacOSZstd is a no-op on non-Darwin platforms.
// The actual implementation is in hostsetup_darwin.go.
func EnsureMacOSZstd() error { return nil }
