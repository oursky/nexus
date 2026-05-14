//go:build darwin

package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/macvm"
	"github.com/oursky/nexus/packages/nexus/internal/infra/store"
	"github.com/oursky/nexus/packages/nexus/internal/transport"
)

// buildLibkrunDriver builds the macOS libkrun driver bundle.
func buildLibkrunDriver(cfg Config, _ *store.WorkspaceStore, _ *transport.Hub) (libkrunDriverBundle, error) {
	shareDir := macvmShareDir()
	libDir := filepath.Join(shareDir, "lib")
	for _, name := range []string{"libkrun.dylib", "libkrunfw.dylib"} {
		p := filepath.Join(libDir, name)
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				return libkrunDriverBundle{}, ErrLibkrunNotBootstrapped
			}
			return libkrunDriverBundle{}, err
		}
	}

	// VM state (per-workspace dirs, hvc0.log, gvproxy.log) lives under the cache
	// dir (same defaults as macvm.DefaultManagerConfig), not under the lib
	// share dir — otherwise CI and docs that assume ~/.cache/nexus/macvm-workspaces
	// miss logs after failures.
	macDefaults := macvm.DefaultManagerConfig()
	driver := macvm.NewDriver(macvm.ManagerConfig{
		LibDir:          libDir,
		VMWorkDir:       macDefaults.VMWorkDir,
		RootFSCachePath: cfg.RootFSPath,
		EmbeddedAgentFn: cfg.EmbeddedAgentFn,
	})
	return libkrunDriverBundle{rtDriver: driver, macCtl: driver}, nil
}

// macvmShareDir returns the data directory for macOS VM assets.
func macvmShareDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus")
}

// libkrunDriverBundle holds the darwin runtime adapter and driver.
type libkrunDriverBundle struct {
	rtDriver domainruntime.Driver
	macCtl   *macvm.Driver
}

func (b libkrunDriverBundle) CleanupStaleInstances(_ context.Context) error { return nil }

func (b libkrunDriverBundle) AsDriver() domainruntime.Driver { return b.rtDriver }

func (b libkrunDriverBundle) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	return b.macCtl.AgentConn(ctx, workspaceID)
}

func (b libkrunDriverBundle) GuestWorkdir(workspaceID string) string {
	return b.macCtl.GuestWorkdir(workspaceID)
}

func (b libkrunDriverBundle) DialPort(ctx context.Context, workspaceID string, port int) (net.Conn, error) {
	return b.macCtl.DialPort(ctx, workspaceID, port)
}

func (b libkrunDriverBundle) WorkspaceReady(ctx context.Context, workspaceID string) (bool, error) {
	return b.macCtl.WorkspaceReadyByID(ctx, workspaceID)
}

func (b libkrunDriverBundle) SerialLogPath(workspaceID string) (string, error) {
	return b.macCtl.SerialLogPath(workspaceID)
}

// prewarmLibkrunBaseImages is a no-op on darwin; no bake infrastructure yet.
func prewarmLibkrunBaseImages(_ *store.WorkspaceStore, _ string) {}
