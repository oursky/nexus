//go:build linux || darwin

package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	startcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
	"github.com/oursky/nexus/packages/nexus/internal/buildinfo"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/vmrootfs"
	"github.com/spf13/cobra"
)

// installOpts holds the parsed install command options.
type installOpts struct {
	noRestart bool
	release   string
	rootfsURL string
	skipBake  bool
	timeout   string
	stderr    interface{ Write([]byte) (int, error) }
	stdout    interface{ Write([]byte) (int, error) }
}

func runInstall(cmd *cobra.Command, noRestart bool, release, rootfsURL string, skipBake bool, timeout string) error {
	opts := installOpts{
		noRestart: noRestart,
		release:   release,
		rootfsURL: rootfsURL,
		skipBake:  skipBake,
		timeout:   timeout,
		stderr:    cmd.ErrOrStderr(),
		stdout:    cmd.OutOrStdout(),
	}

	// Phase 1: Platform-specific host setup (tools, deps, data dirs, etc.)
	if err := runPlatformSetup(opts); err != nil {
		return fmt.Errorf("install: host setup: %w", err)
	}

	// Phase 2: Rootfs download
	if err := ensureRootFS(opts); err != nil {
		return fmt.Errorf("install: rootfs: %w", err)
	}

	// Phase 3: Bake (platform-specific)
	if !opts.skipBake {
		if err := runPlatformBake(opts); err != nil {
			fmt.Fprintf(opts.stderr, "install: bake: %v (non-fatal)\n", err)
		}
	}

	// Phase 4: Write release stamp
	if err := writeReleaseStamp(opts); err != nil {
		fmt.Fprintf(opts.stderr, "install: release stamp: %v (non-fatal)\n", err)
	}

	fmt.Fprintln(opts.stdout, "install: complete")

	// Phase 5: Restart daemon
	if !opts.noRestart {
		if err := restartDaemonPostInstall(); err != nil {
			fmt.Fprintf(opts.stderr, "install: daemon restart failed (non-fatal): %v\n", err)
			fmt.Fprintf(opts.stderr, "install: run 'nexus daemon restart' manually\n")
		}
	}

	return nil
}

// ensureRootFS downloads the rootfs to the appropriate cache location.
func ensureRootFS(opts installOpts) error {
	destPath := vmRootFSPath()

	var urls []string
	if opts.rootfsURL != "" {
		urls = []string{opts.rootfsURL}
	} else if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		urls = vmrootfs.LinuxAMD64GuestRootFSDownloadCandidates()
	} else if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		urls = vmrootfs.MacOSGuestRootFSDownloadCandidates()
	}

	if len(urls) == 0 {
		fmt.Fprintf(opts.stderr, "install: no rootfs download URLs for %s/%s (skipped)\n", runtime.GOOS, runtime.GOARCH)
		return nil
	}

	fmt.Fprintf(opts.stderr, "install: ensuring rootfs at %s...\n", destPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := vmrootfs.EnsureRootFS(ctx, destPath, urls, ""); err != nil {
		return fmt.Errorf("rootfs download: %w", err)
	}
	fmt.Fprintf(opts.stderr, "install: rootfs ready at %s\n", destPath)
	return nil
}

// writeReleaseStamp writes the release version to the stamp file so daemon
// start can detect a stale rootfs when the binary is updated.
// If release is empty, derives it from buildinfo.Version (skips for "dev" builds).
func writeReleaseStamp(opts installOpts) error {
	release := opts.release
	if release == "" {
		release = buildinfo.Version
		if release == "" || release == "dev" {
			return nil // dev builds don't stamp
		}
	}
	stampDir := startcmd.DefaultDataDir()
	stampFile := rootFSReleaseStampPath(stampDir)
	fmt.Fprintf(opts.stderr, "install: writing release stamp %s → %s\n", release, stampFile)
	return os.WriteFile(stampFile, []byte(release), 0o644)
}

// rootFSReleaseStampPath returns the path to the rootfs-release stamp file.
func rootFSReleaseStampPath(stampDir string) string {
	return stampDir + "/rootfs-release"
}

// restartNexusDaemon stops and starts the nexus daemon.
// Used by platform-specific restart functions.
func restartNexusDaemon() error {
	stopCmd := exec.Command("nexus", "daemon", "stop")
	stopCmd.Stdout = nil
	stopCmd.Stderr = nil
	_ = stopCmd.Run()

	startCmd := exec.Command("nexus", "daemon", "start")
	startCmd.Stdout = nil
	startCmd.Stderr = nil
	return startCmd.Run()
}
