//go:build linux

package install

import (
	"fmt"

	startcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/hostsetup"
)

func runPlatformSetup(opts installOpts) error {
	fmt.Fprintln(opts.stderr, "install: checking Linux host prerequisites...")

	// Guest rootfs tools (e2fsprogs, coreutils)
	if err := hostsetup.EnsureLinuxRootfsTools(); err != nil {
		return fmt.Errorf("rootfs tools: %w", err)
	}

	// zstd for compressed rootfs downloads
	if err := hostsetup.EnsureLinuxZstd(); err != nil {
		fmt.Fprintf(opts.stderr, "install: zstd: %v (non-fatal)\n", err)
	}

	// Data directory (/data/nexus)
	if err := hostsetup.EnsureLinuxDaemonDataDir(); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}

	// Reflink volume (XFS loopback if needed)
	if err := hostsetup.EnsureLinuxReflinkVolume(); err != nil {
		fmt.Fprintf(opts.stderr, "install: reflink volume: %v (non-fatal)\n", err)
	}

	// Host prerequisites (KVM, user namespaces, AppArmor)
	if err := hostsetup.EnsureLinuxHostPrereqs(); err != nil {
		fmt.Fprintf(opts.stderr, "install: host prereqs: %v (non-fatal)\n", err)
	}

	// passt binary
	if err := hostsetup.EnsurePasst(); err != nil {
		fmt.Fprintf(opts.stderr, "install: passt: %v (non-fatal)\n", err)
	}

	return nil
}

func runPlatformBake(opts installOpts) error {
	fmt.Fprintln(opts.stderr, "install: Linux bake handled by nexus daemon start (skipped)")
	return nil
}

func restartDaemonPostInstall() error {
	return restartNexusDaemon()
}

// vmRootFSPath returns the Linux rootfs cache path.
func vmRootFSPath() string {
	return startcmd.XDGVMAsset("rootfs.ext4")
}
