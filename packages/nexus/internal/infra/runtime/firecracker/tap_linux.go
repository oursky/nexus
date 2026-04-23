//go:build linux

package firecracker

// tap_linux.go previously contained the privileged bridge/tap-helper networking.
// Rootless networking is now implemented in passt_linux.go using passt + /dev/net/tun.
// This file is kept as a placeholder to avoid breaking build tags.
