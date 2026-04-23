//go:build linux && arm64 && !libkrun

package main

import _ "embed"

// embeddedFirecracker contains the official Firecracker binary for linux/arm64.
// It is embedded at build time so that `nexus daemon start` can install
// Firecracker automatically without requiring it in PATH.
//
// To refresh the embedded binary run:
//
//	task firecracker:update
//
// which downloads the latest release from GitHub and writes it here, then:
//
//	go generate ./cmd/nexus
//
//go:embed firecracker-linux-arm64
var embeddedFirecracker []byte
