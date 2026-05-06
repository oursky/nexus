package workspace

import (
	"fmt"
	"path/filepath"

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
			explicitOut := outPath != ""
			if outPath == "" {
				outPath = args[0]
			}
			// Only append .nxbundle when the user did not supply --out.
			if !explicitOut && filepath.Ext(outPath) != ".nxbundle" {
				outPath += ".nxbundle"
			}
			exp := bundle.NewExporter()
			bundlePath, err := exp.Export(cmd.Context(), args[0], outPath)
			if err != nil {
				return fmt.Errorf("workspace export: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "exported workspace %s to %s\n", args[0], bundlePath)
			fmt.Fprintf(cmd.OutOrStdout(), "run with: %s\n", bundlePath)
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "output path (default: <workspace>.nxbundle)")
	return cmd
}
