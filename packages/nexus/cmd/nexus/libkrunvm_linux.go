//go:build linux && libkrun

package main

import (
	libkrunvmcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/libkrunvm"
)

func init() {
	extraCommands = append(extraCommands, libkrunvmcmd.Command())
}
