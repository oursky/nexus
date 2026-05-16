//go:build darwin

package vm

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	startcmd "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
	macruntime "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/macvm"
	"github.com/spf13/cobra"
)

func bakeCommand() *cobra.Command {
	var timeoutStr string
	var force bool
	cmd := &cobra.Command{
		Use:   "bake",
		Short: "Pre-bake developer tools into the base rootfs",
		Long: `Boots a temporary libkrun VM using Hypervisor.framework and gvproxy networking,
runs the embedded Linux guest-agent in bake mode (apt toolchain + mise + Docker tooling),
then replaces the cached base rootfs at ~/.cache/nexus/vm/rootfs.ext4.

Use --force to delete any existing bake stamp and re-bake from scratch.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDarwinBake(cmd.OutOrStdout(), cmd.ErrOrStderr(), timeoutStr, force)
		},
	}
	cmd.Flags().StringVar(&timeoutStr, "timeout", "", "Outer wall-clock limit for the bake command (go duration; defaults to baked-in VM/network timeout)")
	cmd.Flags().BoolVar(&force, "force", false, "Delete existing bake stamp and force a fresh bake")
	return cmd
}

func runDarwinBake(stdout, stderr io.Writer, timeoutStr string, force bool) error {
	if startcmd.StartSetupFn != nil {
		fmt.Fprintln(stderr, "vm bake: ensuring Hypervisor.framework + libkrun assets...")
		if err := startcmd.StartSetupFn(stderr); err != nil {
			return fmt.Errorf("bootstrap failed: %w", err)
		}
		fmt.Fprintln(stderr, "vm bake: prerequisites ready")
	}

	startcmdFn := startcmd.EmbeddedAgentFn
	if startcmdFn == nil || len(startcmdFn()) == 0 {
		return fmt.Errorf("embedded linux guest-agent is empty — rebuild the nexus CLI after compiling cmd/nexus-guest-agent (see embed_agent_darwin_* go:generate stubs)")
	}

	mc := macruntime.DefaultManagerConfig()
	rootfsPath := mc.RootFSCachePath
	if _, err := os.Stat(rootfsPath); err != nil {
		fmt.Fprintf(stderr, "vm bake: fetching base guest rootfs (missing at %s)...\n", rootfsPath)
		ctxFetch, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if prefetchErr := macruntime.EnsureRootFS(ctxFetch, mc); prefetchErr != nil {
			return fmt.Errorf(
				"download base rootfs into %s: %w\nProvide an un-baked ubuntu-based ext4 locally (same layout as linux guest-rootfs artifacts) before running bake.",
				rootfsPath, prefetchErr)
		}
	}

	mc.LibDir = filepath.Clean(mc.LibDir)
	if mc.LibDir == "" {
		return fmt.Errorf("libkrun lib dir empty — run `nexus daemon start` once or task stage:libkrun-macos")
	}
	if fi, err := os.Stat(mc.LibDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("libkrun dylibs directory missing (%s)", mc.LibDir)
	}

	timeout, err := parseBakeOuterTimeout(timeoutStr)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "vm bake: baking base rootfs (outer timeout=%s)...\n", timeout)

	stampDir := startcmd.DefaultDataDir()
	if force {
		fmt.Fprintf(stderr, "vm bake: --force: deleting existing bake stamp...\n")
		macruntime.DeleteBakeStamp(stampDir)
	}

	cfg := macruntime.BakeMacConfig{
		LibDir:          mc.LibDir,
		RootFSBasePath:  rootfsPath,
		EmbeddedAgentFn: startcmdFn,
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if bakeErr := macruntime.BakeRootfsIfNeeded(ctx, cfg, stampDir); bakeErr != nil {
		return fmt.Errorf("bake failed: %w", bakeErr)
	}
	fmt.Fprintf(stdout, "vm bake: complete — base guest rootfs ready at %s\n", rootfsPath)
	return nil
}
