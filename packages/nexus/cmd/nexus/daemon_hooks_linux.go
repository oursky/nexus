//go:build linux

package main

import (
	"io"

	daemoncmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon"
)

func init() {
	// Wire the rootless bootstrap as the setup function for `nexus daemon start`.
	// This replaces the old privileged runSetupFirecracker path.
	daemoncmd.StartSetupFn = func(w io.Writer, driver string) error {
		return RunRootlessBootstrap(w, false, driver)
	}
	daemoncmd.StartSetupFnJSON = func(w io.Writer, driver string) error {
		return RunRootlessBootstrap(w, true, driver)
	}

	// Expose the embedded agent bytes for agent injection.
	daemoncmd.EmbeddedAgentFn = func() []byte { return embeddedAgent }

	// Wire the privileged teardown function for `nexus daemon implode`.
	daemoncmd.ImplodePrivilegedFn = runImplodePrivileged

	// Wire the unprivileged driver cleanup (kills libkrun/passt orphans).
	daemoncmd.ImplodeUserCleanupFn = killLibkrunOrphans
}
