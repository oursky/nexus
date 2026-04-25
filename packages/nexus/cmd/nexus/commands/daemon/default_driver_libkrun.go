//go:build linux

package daemon

// defaultDriver is "libkrun" on Linux — the primary VM backend.
// The daemon auto-installs libkrun assets during bootstrap and prefers libkrun
// unless explicitly overridden with --driver.
const defaultDriver = "libkrun"
