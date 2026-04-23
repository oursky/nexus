//go:build linux

package main

import (
	"io"

	daemoncmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon"
)

func init() {
	// Wire the rootless bootstrap as the setup function for `nexus daemon start`.
	// This replaces the old privileged runSetupFirecracker path.
	daemoncmd.StartSetupFn = func(w io.Writer) error {
		return RunRootlessBootstrap(w, false)
	}
	daemoncmd.StartSetupFnJSON = func(w io.Writer) error {
		return RunRootlessBootstrap(w, true)
	}

	// Expose the embedded agent bytes for agent injection.
	daemoncmd.EmbeddedAgentFn = func() []byte { return embeddedAgent }

	// Wire the privileged teardown function for `nexus daemon implode`.
	daemoncmd.ImplodePrivilegedFn = runImplodePrivileged
}
