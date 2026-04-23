//go:build !linux

package daemon

import (
	"context"
	"fmt"
	"net"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/infra/store"
)

// buildLibkrunDriver is a no-op stub on non-Linux platforms.
func buildLibkrunDriver(_ Config) (libkrunDriverBundle, error) {
	return libkrunDriverBundle{}, fmt.Errorf(
		"libkrun driver not available on this platform",
	)
}

// libkrunDriverBundle holds the runtime adapter and driver.
// On non-Linux builds this is an empty shim.
type libkrunDriverBundle struct {
	rtDriver domainruntime.Driver
}

func (b libkrunDriverBundle) CleanupStaleInstances(_ context.Context) error { return nil }
func (b libkrunDriverBundle) AsDriver() domainruntime.Driver                { return b.rtDriver }

func (b libkrunDriverBundle) AgentConn(_ context.Context, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("libkrun not available on this platform")
}
func (b libkrunDriverBundle) GuestWorkdir(_ string) string { return "/workspace" }
func (b libkrunDriverBundle) DialPort(_ context.Context, _ string, _ int) (net.Conn, error) {
	return nil, fmt.Errorf("libkrun not available on this platform")
}
func (b libkrunDriverBundle) WorkspaceReady(_ context.Context, _ string) (bool, error) {
	return false, fmt.Errorf("libkrun not available on this platform")
}

// prewarmLibkrunBaseImages is a no-op stub on non-Linux platforms.
func prewarmLibkrunBaseImages(_ *store.WorkspaceStore, _ string) {}
