//go:build !(linux && libkrun)

//nolint:unused
package main

// Stubs for non-libkrun builds. The embedded VM binary and shared libraries
// are only available when built with -tags libkrun on Linux.
var (
	embeddedLibkrunVM []byte
	embeddedLibkrun   []byte
	embeddedLibkrunfw []byte
)
