package workspace

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/internal/domain/bundle"
	"github.com/spf13/cobra"
)

func exportCommand() *cobra.Command {
	var outPath string

	cmd := &cobra.Command{
		Use:   "export <workspace>",
		Short: "Export a workspace to a .nxbundle archive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if outPath == "" {
				outPath = args[0]
			}
			exp := bundle.NewExporter()
			runnerPath, err := exp.Export(cmd.Context(), args[0], outPath)
			if err != nil {
				return fmt.Errorf("workspace export: %w", err)
			}
			// Normalise the displayed path to include extension.
			displayPath := outPath
			if len(displayPath) < len(".nxbundle") || displayPath[len(displayPath)-len(".nxbundle"):] != ".nxbundle" {
				displayPath += ".nxbundle"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "exported workspace %s to %s\n", args[0], displayPath)
			fmt.Fprintf(cmd.OutOrStdout(), "exported runner stub to %s\n", runnerPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "output path (default: <workspace>.nxbundle)")
	return cmd
}
