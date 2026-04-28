//go:build !linux

package daemon

// defaultDriver is "process" on non-Linux platforms (macOS/Windows use sandbox mode).
const defaultDriver = "process"
