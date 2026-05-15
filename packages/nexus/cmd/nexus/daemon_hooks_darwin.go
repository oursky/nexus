//go:build darwin

package main

import (
	"io"

	startcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
)

func init() {
	startcmd.StartSetupFn = func(w io.Writer) error {
		return RunDarwinBootstrap(w, false)
	}
	startcmd.StartSetupFnJSON = func(w io.Writer) error {
		return RunDarwinBootstrap(w, true)
	}
	// Mirrors Linux daemon hooks: exposes embedded guest-agent bytes for `nexus vm bake`
	// and other rootfs tooling.
	startcmd.EmbeddedAgentFn = func() []byte { return embeddedAgent }
}
