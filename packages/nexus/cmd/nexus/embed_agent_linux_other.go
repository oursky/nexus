//go:build linux && !amd64 && !arm64

package main

// embeddedAgent is nil on uncommon Linux architectures.
// VM bootstrap will build nexus-guest-agent from source in this case.
var embeddedAgent []byte
