//go:build darwin && amd64

package main

// Intel Mac builds are unsupported: bundled libkrun/kernel payloads target Apple Silicon hosts.
var (
	embeddedLibkrunDylib   []byte
	embeddedLibkrunfwDylib []byte
	embeddedKernel         []byte
	embeddedAgent          []byte
)
