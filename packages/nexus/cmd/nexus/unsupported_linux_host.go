//go:build linux && !amd64

// Non-amd64 Linux hosts are not supported as libkrun VM hosts; empty embed blobs.
//
// Naming: avoid filename patterns like *_linux_*amd64.go — cmd/go treats them as OS/arch
// filename constraints and ignores the source even when explicit //go:build tags match.
package main

var (
	embeddedAgent     []byte
	embeddedLibkrunVM []byte
	embeddedLibkrun   []byte
	embeddedLibkrunfw []byte
	embeddedPasst     []byte
)
