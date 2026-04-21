package mutagen

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/mutagenbin"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mutagen",
		Short: "Mutagen tooling bundled with nexus",
	}
	cmd.AddCommand(pathCommand())
	return cmd
}

func pathCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the absolute path to the mutagen binary nexus uses (embedded on macOS and Linux amd64/arm64)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mutagenbin.Path()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), p)
			return err
		},
	}
}
