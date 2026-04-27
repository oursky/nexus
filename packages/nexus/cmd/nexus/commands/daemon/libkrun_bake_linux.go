//go:build linux

package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	lkruntime "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
)

func init() {
	LibkrunBakeFn = ensureLibkrunRootfsBaked
}

// ensureLibkrunRootfsBaked pre-installs developer tools into the base rootfs
// by booting a temporary VM in "bake mode". Idempotent: a host-side stamp
// file prevents re-baking on every daemon restart.
//
// Runs in the parent process (before self-daemonization) so the operator
// sees progress. Non-fatal: logs and continues on failure — the agent will
// still install tools on first VM boot.
func ensureLibkrunRootfsBaked(rootfsPath, kernelPath string, emit func(status, message string)) {
	report := func(status, message string) {
		if emit != nil {
			emit(status, message)
		}
	}

	stampDir := defaultDataDir()
	report("start", "checking cached rootfs bake")

	home, _ := os.UserHomeDir()
	vmBin := filepath.Join(home, ".local", "share", "nexus", "bin", "nexus-libkrun-vm")
	if _, err := os.Stat(vmBin); err != nil {
		log.Printf("daemon: rootfs bake: nexus-libkrun-vm not found at %s — skipping bake", vmBin)
		report("ok", "skipped (nexus-libkrun-vm missing)")
		return
	}

	cfg := lkruntime.ManagerConfig{
		LibkrunVMBin:    vmBin,
		KernelPath:      kernelPath,
		RootFSBasePath:  rootfsPath,
		NetworkBackend:  "tsi",
		EmbeddedAgentFn: EmbeddedAgentFn,
	}

	ctx := context.Background()
	report("start", "pre-baking rootfs toolchain (this can take several minutes)")
	if err := lkruntime.BakeRootfsIfNeeded(ctx, cfg, stampDir); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: rootfs bake warning (non-fatal): %v\n", err)
		fmt.Fprintf(os.Stderr, "  The first workspace start will install tools in-VM (slower).\n")
		report("error", "bake failed, fallback to first-boot in-VM install")
		return
	}
	report("ok", "base rootfs bake ready")
}
