//go:build linux

package main

import (
	daemoncmd "github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/daemon"
)

func init() {
	// Wire the Firecracker host-setup function so `nexus daemon start`
	// auto-provisions prerequisites on fresh Linux machines.
	daemoncmd.StartSetupFn = runSetupFirecracker

	// Expose the embedded agent bytes for agent injection.
	daemoncmd.EmbeddedAgentFn = func() []byte { return embeddedAgent }

	// Wire the privileged teardown function for `nexus daemon implode`.
	daemoncmd.ImplodePrivilegedFn = runImplodePrivileged
}
