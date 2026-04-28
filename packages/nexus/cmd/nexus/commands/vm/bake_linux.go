//go:build linux

package vm

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	startcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
	lkruntime "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
	"github.com/spf13/cobra"
)

func bakeCommand() *cobra.Command {
	var timeoutStr string
	cmd := &cobra.Command{
		Use:   "bake",
		Short: "Pre-bake developer tools into the base rootfs",
		Long: `Boots a temporary VM and installs developer tools (make, git, docker, nodejs,
npm, etc.) into the base rootfs so that workspace starts are instant.

Idempotent: skips if the bake stamp is already present.

This is the same bake that happens automatically during 'nexus daemon start',
but exposed as an explicit command for better visibility and debugging.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBake(cmd.OutOrStdout(), cmd.ErrOrStderr(), timeoutStr)
		},
	}
	cmd.Flags().StringVar(&timeoutStr, "timeout", "10m", "Max time to wait for bake completion")
	return cmd
}

func runBake(stdout, stderr io.Writer, timeoutStr string) error {
	// Ensure bootstrap has run so kernel, passt, and libkrun libs are present.
	if startcmd.StartSetupFn != nil {
		fmt.Fprintln(stderr, "vm bake: ensuring host prerequisites...")
		if err := startcmd.StartSetupFn(stderr, "libkrun"); err != nil {
			return fmt.Errorf("host setup failed: %w", err)
		}
		fmt.Fprintln(stderr, "vm bake: host prerequisites ready")
	}

	kernelPath := startcmd.DefaultVMKernelPath
	rootfsPath := startcmd.DefaultVMRootfsPath

	// Sanity-check assets exist.
	for _, path := range []string{kernelPath, rootfsPath} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("VM asset not found at %s: %w", path, err)
		}
	}

	libkrunVMBin := filepath.Join(filepath.Dir(rootfsPath), "..", "bin", "nexus-libkrun-vm")
	if _, err := os.Stat(libkrunVMBin); err != nil {
		// Fallback to the canonical path under XDG_DATA_HOME.
		home, _ := os.UserHomeDir()
		libkrunVMBin = filepath.Join(home, ".local", "share", "nexus", "bin", "nexus-libkrun-vm")
	}
	if _, err := os.Stat(libkrunVMBin); err != nil {
		return fmt.Errorf("nexus-libkrun-vm not found at %s: %w", libkrunVMBin, err)
	}

	// Parse timeout.
	timeout, err := parseDuration(timeoutStr)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeoutStr, err)
	}

	fmt.Fprintf(stderr, "vm bake: pre-baking rootfs toolchain (timeout=%s)...\n", timeout)

	cfg := lkruntime.ManagerConfig{
		LibkrunVMBin:    libkrunVMBin,
		KernelPath:      kernelPath,
		RootFSBasePath:  rootfsPath,
		NetworkBackend:  "tsi",
		EmbeddedAgentFn: startcmd.EmbeddedAgentFn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	stampDir := defaultDataDir()
	if err := lkruntime.BakeRootfsIfNeeded(ctx, cfg, stampDir); err != nil {
		return fmt.Errorf("bake failed: %w", err)
	}

	fmt.Fprintln(stderr, "vm bake: complete — base rootfs is ready")
	return nil
}

func parseDuration(s string) (time.Duration, error) {
	// Allow simple suffixes for convenience.
	switch s {
	case "":
		return 10 * time.Minute, nil
	}
	return time.ParseDuration(s)
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus")
}
