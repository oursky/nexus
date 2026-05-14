//go:build linux && arm64

package main

// No static passt blob for arm64 in the embed pipeline; install passt from the
// distro or copy from PATH during bootstrap (see installPasstRootless).
var embeddedPasst []byte
