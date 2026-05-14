//go:build linux

package main

// The libkrun-vm subcommand has been replaced by the standalone
// nexus-libkrun-vm binary, which is embedded into this binary on linux/amd64
// and extracted to ~/.local/share/nexus/bin/ during daemon bootstrap.
//
// The main nexus daemon no longer links against libkrun.so at load time —
// all libkrun CGO lives exclusively in cmd/nexus-libkrun-vm/.
// The libkrun daemon driver is always compiled in on Linux (no build tag
// required) but requires nexus-libkrun-vm to be installed on the host.
