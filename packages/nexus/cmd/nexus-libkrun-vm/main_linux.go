//go:build linux && cgo

// nexus-libkrun-vm is a standalone process that configures and runs a single
// libkrun microVM. It is spawned by the Nexus daemon for each workspace VM.
//
// It is intentionally separate from the main nexus binary so that the daemon
// binary itself does not need to link against libkrun.so at load time.
// The daemon embeds this binary and extracts it alongside libkrun.so / libkrunfw.so
// to ~/.local/share/nexus/bin/ + ~/.local/share/nexus/lib/ during bootstrap.
//
// Usage:
//
//	nexus-libkrun-vm --config=<path-to-VMSpec-JSON>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
)

func main() {
	configFile := flag.String("config", "", "path to VMSpec JSON file (required)")
	flag.Parse()

	if *configFile == "" {
		fmt.Fprintln(os.Stderr, "nexus-libkrun-vm: --config is required")
		os.Exit(1)
	}

	data, err := os.ReadFile(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus-libkrun-vm: read config: %v\n", err)
		os.Exit(1)
	}

	var spec libkrun.VMSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		fmt.Fprintf(os.Stderr, "nexus-libkrun-vm: parse config: %v\n", err)
		os.Exit(1)
	}

	if err := launchVM(spec); err != nil {
		fmt.Fprintf(os.Stderr, "nexus-libkrun-vm: %v\n", err)
		os.Exit(1)
	}
}
