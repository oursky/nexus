//go:build linux

package main

import (
	"fmt"
	"io"
	"os/exec"
)

// killLibkrunOrphans best-effort kills lingering libkrun helper processes.
// This runs during `nexus daemon implode` user cleanup.
func killLibkrunOrphans(w io.Writer) {
	names := []string{"nexus-libkrun-vm", "passt", "pasta", "nexus-guest-agent"}
	for _, name := range names {
		cmd := exec.Command("pkill", "-x", name)
		if err := cmd.Run(); err != nil {
			// Ignore "no process matched" and keep cleanup resilient.
			continue
		}
		fmt.Fprintf(w, "killed lingering process: %s\n", name)
	}
}
