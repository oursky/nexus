//go:build darwin

package main

import _ "embed"

// embeddedLibkrunDylib is the libkrun.dylib for macOS arm64, embedded at build time.
// Built by: scripts/local/build-libkrun-darwin.sh
//
//go:embed libkrun-darwin-arm64.dylib
var embeddedLibkrunDylib []byte

// embeddedLibkrunfwDylib is the libkrunfw.dylib for macOS arm64.
//
//go:embed libkrunfw-darwin-arm64.dylib
var embeddedLibkrunfwDylib []byte
