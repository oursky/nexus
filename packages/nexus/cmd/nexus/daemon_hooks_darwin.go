//go:build darwin

package main

import (
	"io"

	startcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
)

func init() {
	startcmd.StartSetupFn = func(w io.Writer, driver string) error {
		return RunDarwinBootstrap(w, false, driver)
	}
	startcmd.StartSetupFnJSON = func(w io.Writer, driver string) error {
		return RunDarwinBootstrap(w, true, driver)
	}
}
