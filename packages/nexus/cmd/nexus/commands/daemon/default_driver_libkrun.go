//go:build linux

package daemon

// defaultDriver is "vm" on Linux — the primary VM backend (libkrun).
// The daemon auto-installs libkrun assets during bootstrap and prefers VM
// unless explicitly overridden with --driver.
const defaultDriver = "vm"
