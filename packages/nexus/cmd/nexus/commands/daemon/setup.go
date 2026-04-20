package daemon

import (
	"fmt"

	"github.com/spf13/cobra"
)

// setupCommand returns the "daemon setup" subcommand.
// It provisions the host infrastructure required to run Firecracker VMs:
// network bridge, kernel image, rootfs image, and helper binaries.
//
// On Linux this is a prerequisite for any workspace operation.  In CI it
// must be run once before the e2e test suite so that each test's daemon
// start does not trigger the slow (several-minute) download + conversion
// path inside the harness 20-second readiness deadline.
//
// On other platforms (e.g. macOS) Firecracker is not yet supported and
// the command exits immediately with a no-op message.
func setupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Provision Firecracker host infrastructure (bridge, kernel, rootfs)",
		Long: `Provision the host infrastructure required to run Firecracker VMs.

On Linux this installs the nexus-tap-helper (with cap_net_admin), configures
the nexusbr0 network bridge, and downloads the Firecracker kernel and Ubuntu
rootfs images to /var/lib/nexus/.

This command is idempotent: re-running it after a successful setup is a no-op.

In CI, run this once as a pre-flight step (with sudo) before executing the
e2e test suite so that each per-test daemon start completes within its
readiness deadline.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if StartSetupFn == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No host setup required on this platform.")
				return nil
			}
			return StartSetupFn(cmd.ErrOrStderr())
		},
	}
}
