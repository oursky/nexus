package install

import (
	"github.com/spf13/cobra"
)

// Command returns the "install" cobra command.
func Command() *cobra.Command {
	var (
		noRestart bool
		release   string
		rootfsURL string
		skipBake  bool
		timeout   string
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or refresh Nexus host prerequisites (rootfs, tools, bake)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, noRestart, release, rootfsURL, skipBake, timeout)
		},
	}

	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "skip automatic daemon restart after install")
	cmd.Flags().StringVar(&release, "release", "", "release tag for rootfs download (default: auto-detect)")
	cmd.Flags().StringVar(&rootfsURL, "rootfs-url", "", "direct rootfs URL override")
	cmd.Flags().BoolVar(&skipBake, "skip-bake", false, "do not run bake even if needed")
	cmd.Flags().StringVar(&timeout, "timeout", "10m", "bake timeout")

	return cmd
}
