//go:build !linux && !darwin

package install

import (
	"fmt"

	"github.com/spf13/cobra"
)

func runInstall(cmd *cobra.Command, noRestart bool, release, rootfsURL string, skipBake bool, timeout string) error {
	return fmt.Errorf("nexus install is not supported on this platform (Linux or macOS required)")
}
