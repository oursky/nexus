package workspace

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/internal/domain/bundle"
	"github.com/spf13/cobra"
)

func importCommand() *cobra.Command {
	var dryRun bool

	var fromPath string

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import a workspace from a .nxbundle archive",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromPath == "" {
				return fmt.Errorf("workspace import: --from <bundle> is required")
			}
			imp := bundle.NewImporter()
			if err := imp.Import(cmd.Context(), fromPath, dryRun); err != nil {
				return fmt.Errorf("workspace import: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fromPath, "from", "", "path to the .nxbundle archive to import")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate the bundle and print a compatibility report without importing")
	return cmd
}
