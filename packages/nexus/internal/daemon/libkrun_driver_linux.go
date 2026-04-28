//go:build linux

package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
	lkruntime "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
	"github.com/oursky/nexus/packages/nexus/internal/infra/store"
)

// LibkrunShareBinDir returns the directory where the nexus-libkrun-vm helper
// binary is extracted during bootstrap.
func LibkrunShareBinDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus", "bin")
}

// libkrunShareLibDir returns the directory where libkrun.so and libkrunfw.so
// are extracted during bootstrap.
func libkrunShareLibDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "lib")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus", "lib")
}

// libkrunShareVMDir returns the directory containing VM assets installed by
// rootless bootstrap (kernel, rootfs.ext4, rootfs-dir).
func libkrunShareVMDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "vm")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus", "vm")
}

// validateReflinkSupport checks that the workdir filesystem supports reflink
// clones (XFS with reflink=1 or btrfs). This is required for hybrid mode where
// each workspace gets an O(1) CoW clone of the base rootfs.ext4.
func validateReflinkSupport(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create workdir %s: %w", dir, err)
	}
	src := filepath.Join(dir, ".nexus-reflink-test-src")
	dst := filepath.Join(dir, ".nexus-reflink-test-dst")
	_ = os.Remove(src)
	_ = os.Remove(dst)
	defer func() {
		_ = os.Remove(src)
		_ = os.Remove(dst)
	}()

	if err := os.WriteFile(src, []byte("reflink-test"), 0o644); err != nil {
		return fmt.Errorf("write test file: %w", err)
	}
	out, err := exec.Command("cp", "--reflink=always", src, dst).CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Errorf("reflink clone not supported on %s: %w: %s", dir, err, outStr)
	}
	// GNU coreutils ≥9 returns exit 0 even when reflink fails (falls back to
	// regular copy). Detect the failure by inspecting stderr.
	if strings.Contains(outStr, "failed to clone") {
		return fmt.Errorf("reflink clone not supported on %s: %s", dir, outStr)
	}
	return nil
}

func buildLibkrunDriver(cfg Config) (libkrunDriverBundle, error) {
	nexusBin, _ := os.Executable()

	// VM state (overlays, docker-data images, snapshots) lives under the data dir.
	// Default to /data/nexus/libkrun-vms for O(1) CoW clones via reflink on XFS/btrfs.
	// Falls back to $dataDir/libkrun-vms if /data/nexus is not available.
	workDirRoot := cfg.WorkDirRoot
	if workDirRoot == "" {
		if _, err := os.Stat("/data/nexus"); err == nil {
			workDirRoot = "/data/nexus/libkrun-vms"
		} else {
			workDirRoot = filepath.Join(defaultDataDir(), "libkrun-vms")
		}
	}
	if err := validateReflinkSupport(workDirRoot); err != nil {
		return libkrunDriverBundle{}, fmt.Errorf("libkrun requires XFS/btrfs with reflink support for the workdir: %w", err)
	}

	// Prefer the extracted standalone nexus-libkrun-vm binary (set during bootstrap
	// when the binary was built with embedded artifacts). Fall back to the nexus
	// binary itself (smolvm / dev builds that register "libkrun-vm" as a subcommand).
	shareDir := LibkrunShareBinDir()
	libkrunVMBin := filepath.Join(shareDir, "nexus-libkrun-vm")
	if _, err := os.Stat(libkrunVMBin); err != nil {
		libkrunVMBin = "" // not present; manager falls back to NexusBin subcommand
	}

	rootFSPath := strings.TrimSpace(cfg.RootFSPath)
	kernelPath := strings.TrimSpace(cfg.KernelPath)

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
		LibkrunLibDir:   libkrunShareLibDir(),
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
		agent:    adapter,
		vm:       driver,
	}, nil
}

// agentDialer matches the subset of lkruntime.Adapter used by the daemon bundle.
type agentDialer interface {
	AgentConn(ctx context.Context, workspaceID string) (net.Conn, error)
	GuestWorkdir(workspaceID string) string
	DialPort(ctx context.Context, workspaceID string, remotePort int) (net.Conn, error)
}

// vmDriver matches the subset of lkruntime.Driver used by the daemon bundle.
type vmDriver interface {
	CleanupStaleInstances(ctx context.Context) error
	WorkspaceReady(ctx context.Context, workspaceID string) (bool, error)
	SerialLogPath(workspaceID string) (string, error)
}

type libkrunDriverBundle struct {
	rtDriver domainruntime.Driver
	agent    agentDialer
	vm       vmDriver
}

func (b libkrunDriverBundle) AsDriver() domainruntime.Driver { return b.rtDriver }

func (b libkrunDriverBundle) CleanupStaleInstances(ctx context.Context) error {
	if b.vm == nil {
		return nil
	}
	return b.vm.CleanupStaleInstances(ctx)
}

func (b libkrunDriverBundle) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	return b.agent.AgentConn(ctx, workspaceID)
}

func (b libkrunDriverBundle) GuestWorkdir(workspaceID string) string {
	return b.agent.GuestWorkdir(workspaceID)
}

func (b libkrunDriverBundle) WorkspaceReady(ctx context.Context, workspaceID string) (bool, error) {
	if b.vm == nil {
		return false, fmt.Errorf("libkrun driver is unavailable")
	}
	return b.vm.WorkspaceReady(ctx, workspaceID)
}

func (b libkrunDriverBundle) DialPort(ctx context.Context, workspaceID string, port int) (net.Conn, error) {
	return b.agent.DialPort(ctx, workspaceID, port)
}

func (b libkrunDriverBundle) SerialLogPath(workspaceID string) (string, error) {
	if b.vm == nil {
		return "", fmt.Errorf("libkrun driver is unavailable")
	}
	return b.vm.SerialLogPath(workspaceID)
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
			manifestHash := config.ComputeManifestHash(
				config.BaseNexusfilePath(),
				filepath.Join(projectRoot, "Nexusfile"),
				lkruntime.BakeStampVersion,
			)
			_, err := lkruntime.EnsureBaseImage(context.Background(), projectRoot, basesDir, manifestHash)
			if err != nil {
				log.Printf("daemon: prewarm base image for %s: %v", projectRoot, err)
			} else {
				log.Printf("daemon: prewarm base image ready for %s", projectRoot)
			}
		}(root)
	}
}
