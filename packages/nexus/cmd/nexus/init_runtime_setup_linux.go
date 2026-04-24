//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

var initRuntimeBootstrapRunner func(projectRoot, runtimeName string) error = runInitRuntimeBootstrapLinux

var (
	// initRuntimeBootstrapIsRootFn reports whether the current process is root.
	initRuntimeBootstrapIsRootFn = func() bool { return os.Geteuid() == 0 }

	// initRuntimeBootstrapSudoOKFn reports whether sudo is available in PATH.
	initRuntimeBootstrapSudoOKFn = func() bool {
		_, err := exec.LookPath("sudo")
		return err == nil
	}

	// initRuntimeBootstrapIsTTYFn reports whether f is an interactive terminal.
	initRuntimeBootstrapIsTTYFn = func(f *os.File) bool {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		return (fi.Mode() & os.ModeCharDevice) != 0
	}

	// initRuntimeBootstrapSkipFastFailFn, when non-nil and returning true,
	// bypasses the non-interactive fast-fail check (used by the init command
	// when the user explicitly requested setup).
	initRuntimeBootstrapSkipFastFailFn func() bool = nil
)

func runInitRuntimeBootstrapLinux(projectRoot, runtimeName string) error {
	switch runtimeName {
	case "libkrun":
		skipFastFail := initRuntimeBootstrapSkipFastFailFn != nil && initRuntimeBootstrapSkipFastFailFn()
		isRoot := initRuntimeBootstrapIsRootFn()

		// Fast-fail: if unprivileged, no sudo, and not interactive, return a clear
		// error with manual steps rather than running the full bootstrap (which
		// requires root and would fail with an opaque error).
		if !skipFastFail && !isRoot && !initRuntimeBootstrapSudoOKFn() && !initRuntimeBootstrapIsTTYFn(os.Stdin) {
			return fmt.Errorf(
				"runtime setup failed: bootstrap setup failed\n\nmanual next steps:\n  sudo -E nexus init --project-root %s",
				projectRoot,
			)
		}

		// When already privileged, do a quick pre-check: if setup is already
		// complete but only a KVM group refresh is needed, handle it gracefully
		// rather than running the full (slow) bootstrap again.
		if isRoot || skipFastFail {
			if err := setupVerifyFn(); err == nil {
				return nil // already fully configured
			} else if errors.Is(err, errKVMGroupRefreshNeeded) {
				// Try to reexec under the kvm group; if it fails, a root user can
				// access /dev/kvm directly, so the setup is still usable.
				_ = setupKVMGroupReexecFn("")
				return nil
			}
		}

		return RunRootlessBootstrap(io.Discard, false, runtimeName)
	default:
		return nil
	}
}
