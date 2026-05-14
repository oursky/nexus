//go:build !linux && !darwin

package start

// DefaultDriver is "process" on non-Linux platforms (macOS/Windows use sandbox mode).
const DefaultDriver = "process"
