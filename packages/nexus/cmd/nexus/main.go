package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	daemoncmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon"
	projectcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/project"
	spotlightcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/spotlight"
	vmcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/vm"
	workspacecmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/workspace"
)

// extraCommands holds optional cobra commands registered by platform-specific
// init() functions (e.g. the hidden "libkrun-vm" subcommand on Linux+libkrun).
var extraCommands []*cobra.Command

func main() {
	root := &cobra.Command{
		Use:           "nexus",
		Short:         "Nexus remote workspace CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		daemoncmd.Command(),
		workspacecmd.Command(),
		spotlightcmd.Command(),
		projectcmd.Command(),
		vmcmd.Command(),
	)
	for _, cmd := range extraCommands {
		root.AddCommand(cmd)
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

//nolint:unused // used by setup_linux.go (go:build linux)
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n\r\"'`$\\") {
		return strconv.Quote(value)
	}
	return value
}
