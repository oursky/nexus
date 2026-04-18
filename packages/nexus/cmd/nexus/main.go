// Package main is the Nexus unified CLI entry point.
package main

import (
	"fmt"
	"os"

	daemoncmd "github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/daemon"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "nexus",
	Short:         "Nexus remote workspace CLI",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(daemoncmd.Command())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
