//go:build linux

//nolint:unused
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// setupVerifyFn verifies that the daemon setup (libkrun) completed correctly.
// Overridable in tests.
var setupVerifyFn = verifyDaemonSetup

// errKVMGroupRefreshNeeded indicates bootstrap is complete but the current
// session still lacks active /dev/kvm group access.
var errKVMGroupRefreshNeeded = errors.New("kvm group refresh needed")

const setupKVMGroupReexecEnv = "NEXUS_SETUP_KVM_GROUP_REEXEC"

// setupKVMGroupReexecFn re-runs the current nexus command under `sg kvm` so
// group membership takes effect without requiring a full logout/login cycle.
var setupKVMGroupReexecFn = func(commandPath string) error {
	parts := make([]string, 0, len(os.Args))
	parts = append(parts, shellQuote(commandPath))
	for _, arg := range os.Args[1:] {
		parts = append(parts, shellQuote(arg))
	}
	cmd := exec.Command("sg", "kvm", "-c", strings.Join(parts, " "))
	cmd.Env = append(os.Environ(), setupKVMGroupReexecEnv+"=1")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// setupCommandPath returns the path of the nexus binary (used in reexec hints).
func setupCommandPath() string {
	if exe, err := os.Executable(); err == nil {
		exe = strings.TrimSpace(exe)
		if exe != "" {
			return exe
		}
	}
	if len(os.Args) > 0 {
		arg0 := strings.TrimSpace(os.Args[0])
		if arg0 != "" {
			if filepath.IsAbs(arg0) {
				return arg0
			}
			if lp, err := exec.LookPath(arg0); err == nil {
				return lp
			}
			return arg0
		}
	}
	return "nexus"
}

// verifyDaemonSetup checks that the rootless libkrun bootstrap completed.
func verifyDaemonSetup() error {
	home, _ := os.UserHomeDir()
	libDir := filepath.Join(home, ".local", "share", "nexus", "lib")
	soPath := filepath.Join(libDir, "libkrun.so.1")
	if _, err := os.Stat(soPath); err != nil {
		return fmt.Errorf("libkrun.so.1 not found at %s: %w", soPath, err)
	}

	fd, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w: current session lacks read/write access to /dev/kvm", errKVMGroupRefreshNeeded)
		}
		return fmt.Errorf("unable to open /dev/kvm: %w", err)
	}
	_ = fd.Close()
	return nil
}
