//go:build linux

package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	lkruntime "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
	"github.com/oursky/nexus/packages/nexus/internal/infra/store"
)

// libkrunShareBinDir returns the directory where the nexus-libkrun-vm helper
// binary is extracted during bootstrap.
func libkrunShareBinDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus", "bin")
}

func buildLibkrunDriver(cfg Config) (libkrunDriverBundle, error) {
	nexusBin, _ := os.Executable()

	// VM state (overlays, docker-data images, snapshots) lives under the data dir.
	// No XFS or reflink requirement: workspace files are passed through via virtiofs
	// and overlays are sparse ext4 files copied with --sparse=always on fork.
	workDirRoot := cfg.WorkDirRoot
	if workDirRoot == "" {
		workDirRoot = filepath.Join(defaultDataDir(), "libkrun-vms")
	}

	// Prefer the extracted standalone nexus-libkrun-vm binary (set during bootstrap
	// when the binary was built with embedded artifacts). Fall back to the nexus
	// binary itself (smolvm / dev builds that register "libkrun-vm" as a subcommand).
	shareDir := libkrunShareBinDir()
	libkrunVMBin := filepath.Join(shareDir, "nexus-libkrun-vm")
	if _, err := os.Stat(libkrunVMBin); err != nil {
		libkrunVMBin = "" // not present; manager falls back to NexusBin subcommand
	}

	// libkrun hybrid mode requires a block-backed rootfs image.
	rootFSPath := strings.TrimSpace(cfg.RootFSPath)
	kernelPath := strings.TrimSpace(cfg.KernelPath)
	if rootFSPath == "" {
		return libkrunDriverBundle{}, fmt.Errorf("libkrun requires rootfs path (RootFSPath)")
	}
	if kernelPath == "" {
		return libkrunDriverBundle{}, fmt.Errorf("libkrun requires kernel path (KernelPath)")
	}
	if _, err := os.Stat(rootFSPath); err != nil {
		return libkrunDriverBundle{}, fmt.Errorf(
			"libkrun requires rootfs image at %q\n"+
				"  The image is created during 'nexus daemon start' (first run).\n"+
				"  Run: nexus daemon implode && nexus daemon start",
			rootFSPath,
		)
	}
	if _, err := os.Stat(kernelPath); err != nil {
		return libkrunDriverBundle{}, fmt.Errorf("libkrun requires kernel image at %q: %w", kernelPath, err)
	}

	netBackend := strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_NET_BACKEND"))
	if netBackend == "" {
		netBackend = "auto"
	}
	switch netBackend {
	case "auto", "tsi", "virtio-net":
	default:
		return libkrunDriverBundle{}, fmt.Errorf("invalid NEXUS_LIBKRUN_NET_BACKEND=%q (expected auto|tsi|virtio-net)", netBackend)
	}

	log.Printf("daemon: libkrun workdir=%s vmBin=%s kernel=%s rootfs=%s net_backend=%s", workDirRoot, libkrunVMBin, kernelPath, rootFSPath, netBackend)

	lkCfg := lkruntime.ManagerConfig{
		LibkrunVMBin:    libkrunVMBin,
		NexusBin:        nexusBin,
		KernelPath:      kernelPath,
		RootFSBasePath:  rootFSPath,
		BasesDir:        cfg.BasesDir,
		NetworkBackend:  netBackend,
		WorkDirRoot:     workDirRoot,
		EmbeddedAgentFn: cfg.EmbeddedAgentFn,
	}
	driver := lkruntime.NewDriver(lkCfg)
	adapter := lkruntime.NewAdapter(driver)
	return libkrunDriverBundle{
		rtDriver: adapter,
		adapter:  adapter,
		driver:   driver,
	}, nil
}

type libkrunDriverBundle struct {
	rtDriver domainruntime.Driver
	adapter  *lkruntime.Adapter
	driver   *lkruntime.Driver
}

func (b libkrunDriverBundle) AsDriver() domainruntime.Driver { return b.rtDriver }

func (b libkrunDriverBundle) CleanupStaleInstances(ctx context.Context) error {
	if b.driver == nil {
		return nil
	}
	return b.driver.CleanupStaleInstances(ctx)
}

func (b libkrunDriverBundle) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	return b.adapter.AgentConn(ctx, workspaceID)
}

func (b libkrunDriverBundle) GuestWorkdir(workspaceID string) string {
	return b.adapter.GuestWorkdir(workspaceID)
}

func (b libkrunDriverBundle) WorkspaceReady(ctx context.Context, workspaceID string) (bool, error) {
	if b.driver == nil {
		return false, fmt.Errorf("libkrun driver is unavailable")
	}
	return b.driver.WorkspaceReady(ctx, workspaceID)
}

func (b libkrunDriverBundle) DialPort(ctx context.Context, workspaceID string, port int) (net.Conn, error) {
	return b.adapter.DialPort(ctx, workspaceID, port)
}

func (b libkrunDriverBundle) SerialLogPath(workspaceID string) (string, error) {
	if b.driver == nil {
		return "", fmt.Errorf("libkrun driver is unavailable")
	}
	return b.driver.SerialLogPath(workspaceID)
}

// prewarmLibkrunBaseImages builds cached base workspace ext4 images for all
// known libkrun workspaces at daemon startup so the first workspace start
// doesn't block on mkfs.ext4 -d <project_root> (30–120 s for large repos).
// Images are content-addressed by project root path, so this is idempotent.
func prewarmLibkrunBaseImages(wsStore *store.WorkspaceStore, basesDir string) {
	if basesDir == "" {
		return
	}
	ctx := context.Background()
	all, err := wsStore.List(ctx)
	if err != nil {
		log.Printf("daemon: prewarm base images: list workspaces: %v", err)
		return
	}
	seen := map[string]struct{}{}
	for _, ws := range all {
		if ws == nil || ws.Backend != "libkrun" {
			continue
		}
		root := strings.TrimSpace(ws.Repo)
		if root == "" {
			root = strings.TrimSpace(ws.RootPath)
		}
		if root == "" {
			continue
		}
		if _, dup := seen[root]; dup {
			continue
		}
		seen[root] = struct{}{}
		go func(projectRoot string) {
			log.Printf("daemon: prewarm base image for %s", projectRoot)
			_, err := lkruntime.EnsureBaseImage(projectRoot, basesDir)
			if err != nil {
				log.Printf("daemon: prewarm base image for %s: %v", projectRoot, err)
			} else {
				log.Printf("daemon: prewarm base image ready for %s", projectRoot)
			}
		}(root)
	}
}
