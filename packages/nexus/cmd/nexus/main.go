package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	bundlecmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/bundle"
	daemoncmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon"
	projectcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/project"
	spotlightcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/spotlight"
	tuicmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/tui"
	vmcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/vm"
	workspacecmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/workspace"
)

// extraCommands holds optional cobra commands registered by platform-specific
// init() functions (e.g. the hidden "libkrun-vm" subcommand on Linux+libkrun).
var extraCommands []*cobra.Command

func init() {
	// Wire up embedded-kernel extraction so `bundle run` can auto-extract
	// the platform-specific kernel on first use.
	bundlecmd.ExtractEmbeddedKernel = extractEmbeddedKernel
}

func main() {
	root := &cobra.Command{
		Use:           "nexus",
		Short:         "Nexus remote workspace CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// --daemon-ws allows callers (e.g. sandboxed macOS app) to pass the
			// WebSocket URL via CLI arg instead of NEXUS_E2E_DAEMON_WEBSOCKET env var,
			// since macOS App Sandbox may strip env vars from child processes.
			pf := cmd.Root().PersistentFlags()
			if ws, _ := pf.GetString("daemon-ws"); ws != "" {
				os.Setenv("NEXUS_E2E_DAEMON_WEBSOCKET", ws)
			}
			if tok, _ := pf.GetString("daemon-token"); tok != "" {
				os.Setenv("NEXUS_DAEMON_TOKEN", tok)
			}
			if sshHost, _ := pf.GetString("daemon-ssh-host"); sshHost != "" {
				os.Setenv("NEXUS_DAEMON_SSH_HOST", sshHost)
			}
			if sshIdentity, _ := pf.GetString("daemon-ssh-identity"); sshIdentity != "" {
				os.Setenv("NEXUS_DAEMON_SSH_IDENTITY", sshIdentity)
			}
		},
	}
	root.PersistentFlags().String("daemon-ws", "", "daemon WebSocket URL (overrides NEXUS_E2E_DAEMON_WEBSOCKET)")
	root.PersistentFlags().String("daemon-token", "", "daemon bearer token (overrides NEXUS_DAEMON_TOKEN)")
	root.PersistentFlags().String("daemon-ssh-host", "", "SSH host for daemon (overrides NEXUS_DAEMON_SSH_HOST)")
	root.PersistentFlags().String("daemon-ssh-identity", "", "SSH identity file (overrides NEXUS_DAEMON_SSH_IDENTITY)")

	root.AddCommand(
		bundlecmd.Command(),
		daemoncmd.Command(),
		workspacecmd.Command(),
		spotlightcmd.Command(),
		projectcmd.Command(),
		tuicmd.Command(),
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
