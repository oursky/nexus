//go:build linux && amd64

package main

import _ "embed"

// embeddedLibkrunVM is the nexus-libkrun-vm binary, built separately with CGO
// against libkrun.so. Extracted to ~/.local/share/nexus/bin/ at daemon bootstrap.
//
//go:embed nexus-libkrun-vm
var embeddedLibkrunVM []byte

// embeddedLibkrun is the libkrun.so shared library (actual file, not symlink).
// Extracted to ~/.local/share/nexus/lib/ at daemon bootstrap.
//
//go:embed libkrun-embed.so
var embeddedLibkrun []byte

// embeddedLibkrunfw is the libkrunfw.so shared library (actual file, not symlink).
// Extracted to ~/.local/share/nexus/lib/ at daemon bootstrap.
//
//go:embed libkrunfw-embed.so
var embeddedLibkrunfw []byte
