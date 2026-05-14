//go:build linux

package main

import (
	"io"

	daemoncmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon"
	startcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
)

func init() {
	// Wire the rootless bootstrap as the setup function for `nexus daemon start`.
	// This replaces the old privileged VM setup path.
	startcmd.StartSetupFn = func(w io.Writer) error {
		return RunRootlessBootstrap(w, false)
	}
	startcmd.StartSetupFnJSON = func(w io.Writer) error {
		return RunRootlessBootstrap(w, true)
	}

	// Expose the embedded agent bytes for agent injection.
	startcmd.EmbeddedAgentFn = func() []byte { return embeddedAgent }

	// Kill libkrun/passt orphan processes on implode.
	daemoncmd.ImplodeUserCleanupFn = killLibkrunOrphans
}
