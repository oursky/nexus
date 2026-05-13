//go:build darwin

package start

// DefaultDriver is "vm" on macOS when libkrun dylibs are available.
// The user can override with --driver process to use the sandbox backend.
const DefaultDriver = "vm"
