//go:build darwin

package daemon

import (
	"context"
	"fmt"
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
	driver := macvm.NewDriver(macvm.ManagerConfig{
		LibDir:          filepath.Join(shareDir, "lib"),
		VMWorkDir:       filepath.Join(shareDir, "macvm-workspaces"),
		RootFSCachePath: cfg.RootFSPath,
		EmbeddedAgentFn: cfg.EmbeddedAgentFn,
	})
	return libkrunDriverBundle{rtDriver: driver}, nil
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
}

func (b libkrunDriverBundle) CleanupStaleInstances(_ context.Context) error { return nil }
func (b libkrunDriverBundle) AsDriver() domainruntime.Driver                { return b.rtDriver }

func (b libkrunDriverBundle) AgentConn(_ context.Context, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("macvm: agent vsock not supported on darwin; use GuestSSHHost instead")
}
func (b libkrunDriverBundle) GuestWorkdir(_ string) string { return "/workspace" }
func (b libkrunDriverBundle) DialPort(_ context.Context, _ string, _ int) (net.Conn, error) {
	return nil, fmt.Errorf("macvm: DialPort not supported on darwin")
}
func (b libkrunDriverBundle) WorkspaceReady(_ context.Context, _ string) (bool, error) {
	return false, fmt.Errorf("macvm: WorkspaceReady not yet implemented on darwin")
}
func (b libkrunDriverBundle) SerialLogPath(_ string) (string, error) {
	return "", fmt.Errorf("macvm: serial log not yet implemented on darwin")
}

// prewarmLibkrunBaseImages is a no-op on darwin; no bake infrastructure yet.
func prewarmLibkrunBaseImages(_ *store.WorkspaceStore, _ string) {}
