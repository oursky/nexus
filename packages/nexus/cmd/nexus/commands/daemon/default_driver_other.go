//go:build !linux

package daemon

// defaultDriver is empty on non-Linux platforms (macOS/Windows use sandbox mode).
const defaultDriver = ""
