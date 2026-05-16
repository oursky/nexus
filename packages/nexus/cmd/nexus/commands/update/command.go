package update

import (
	"github.com/spf13/cobra"
)

// Command returns the "update" cobra command.
func Command() *cobra.Command {
	var (
		checkOnly bool
		noRestart bool
		release   string
		repo      string
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the nexus binary to the latest GitHub release",
		Long: `Checks GitHub releases for a newer version of nexus.
If a newer version is available, downloads and verifies it,
replaces the current binary atomically, and restarts the daemon.
Use --check to only report availability without performing the update.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, checkOnly, noRestart, release, repo)
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "only check for updates, don't install")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "skip automatic daemon restart after update")
	cmd.Flags().StringVar(&release, "release", "", "specific release tag to update to (default: latest)")
	cmd.Flags().StringVar(&repo, "repo", "oursky/nexus", "GitHub repository (owner/name)")

	return cmd
}
