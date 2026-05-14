//go:build linux && arm64

package main

// Linux arm64: smolvm does not ship libkrun in its public tarball; leave embeds
// empty so installs use distro/Nix libs (install.sh) or CI staging on native arm.
var (
	embeddedLibkrunVM []byte
	embeddedLibkrun   []byte
	embeddedLibkrunfw []byte
)
