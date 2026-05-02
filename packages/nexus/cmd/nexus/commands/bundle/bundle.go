// Package bundle provides the hidden `nexus bundle run` command used by
// self-executing NXPACK bundles. The bundle group is hidden from help
// output — users interact via `nexus workspace export/import` and run
// bundles directly as executables.
package bundle

import (
	"github.com/spf13/cobra"
)

// Command returns the hidden `nexus bundle` cobra command.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "bundle",
		Short:  "Internal commands for self-executing bundles",
		Hidden: true,
	}
	cmd.AddCommand(
		runCommand(),
	)
	return cmd
}
