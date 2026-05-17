//go:build darwin

package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	startcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/hostsetup"
	macruntime "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/macvm"
)

func runPlatformSetup(opts installOpts) error {
	fmt.Fprintln(opts.stderr, "install: checking macOS host prerequisites...")

	if err := hostsetup.EnsureHomebrew(); err != nil {
		return fmt.Errorf("homebrew: %w", err)
	}
	if err := hostsetup.EnsureMacOSRootfsTools(); err != nil {
		return fmt.Errorf("rootfs tools: %w", err)
	}
	if err := hostsetup.EnsureMacOSZstd(); err != nil {
		fmt.Fprintf(opts.stderr, "install: zstd: %v (non-fatal)\n", err)
	}
	return nil
}

func runPlatformBake(opts installOpts) error {
	timeout, err := parseBakeTimeout(opts.timeout)
	if err != nil {
		return err
	}

	stampDir := startcmd.DefaultDataDir()

	// Check if already baked
	if !macruntime.IsBakeStale(stampDir) {
		fmt.Fprintln(opts.stderr, "install: bake stamp current (skipped)")
		return nil
	}

	// Need StartSetupFn for prerequisites
	if startcmd.StartSetupFn != nil {
		fmt.Fprintln(opts.stderr, "install: ensuring Hypervisor.framework + libkrun assets...")
		if err := startcmd.StartSetupFn(opts.stderr); err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
	}

	// Need embedded agent
	agentFn := startcmd.EmbeddedAgentFn
	if agentFn == nil || len(agentFn()) == 0 {
		return fmt.Errorf("embedded linux guest-agent is empty — rebuild nexus CLI")
	}

	mc := macruntime.DefaultManagerConfig()
	rootfsPath := mc.RootFSCachePath

	// Download rootfs if missing
	if _, err := os.Stat(rootfsPath); err != nil {
		fmt.Fprintf(opts.stderr, "install: fetching base rootfs (missing at %s)...\n", rootfsPath)
		ctxFetch, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if prefetchErr := macruntime.EnsureRootFS(ctxFetch, mc); prefetchErr != nil {
			return fmt.Errorf("download base rootfs: %w", prefetchErr)
		}
	}

	mc.LibDir = filepath.Clean(mc.LibDir)
	if mc.LibDir == "" {
		return fmt.Errorf("libkrun lib dir empty — run `nexus daemon start` once first")
	}
	if fi, err := os.Stat(mc.LibDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("libkrun dylibs directory missing (%s)", mc.LibDir)
	}

	fmt.Fprintf(opts.stderr, "install: baking macOS guest rootfs (timeout=%s)...\n", timeout)

	cfg := macruntime.BakeMacConfig{
		LibDir:          mc.LibDir,
		RootFSBasePath:  rootfsPath,
		EmbeddedAgentFn: agentFn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if bakeErr := macruntime.BakeRootfsIfNeeded(ctx, cfg, stampDir); bakeErr != nil {
		return fmt.Errorf("bake failed: %w", bakeErr)
	}

	fmt.Fprintf(opts.stderr, "install: macOS guest rootfs baked at %s\n", rootfsPath)
	return nil
}

func parseBakeTimeout(timeoutStr string) (time.Duration, error) {
	if timeoutStr == "" {
		return 10 * time.Minute, nil
	}
	return time.ParseDuration(timeoutStr)
}

func restartDaemonPostInstall() error {
	return restartNexusDaemon()
}

// vmRootFSPath returns the macOS rootfs cache path.
func vmRootFSPath() string {
	return macruntime.DefaultManagerConfig().RootFSCachePath
}
