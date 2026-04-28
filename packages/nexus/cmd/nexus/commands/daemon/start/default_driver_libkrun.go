//go:build linux

package start

// DefaultDriver is "vm" on Linux — the primary VM backend (libkrun).
// The daemon auto-installs libkrun assets during bootstrap and prefers VM
// unless explicitly overridden with --driver.
const DefaultDriver = "vm"
