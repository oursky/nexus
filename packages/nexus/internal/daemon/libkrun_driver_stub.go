//go:build !linux || !libkrun

package daemon

import (
	"context"
	"fmt"
	"net"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
)

// buildLibkrunDriver is a no-op stub when libkrun build tag is absent.
func buildLibkrunDriver(_ Config) (libkrunDriverBundle, error) {
	return libkrunDriverBundle{}, fmt.Errorf(
		"libkrun driver not available: rebuild with -tags libkrun",
	)
}

// libkrunDriverBundle holds the runtime adapter and driver.
// On non-libkrun builds this is an empty shim.
type libkrunDriverBundle struct {
	rtDriver domainruntime.Driver
}

func (b libkrunDriverBundle) CleanupStaleInstances(_ context.Context) error { return nil }
func (b libkrunDriverBundle) AsDriver() domainruntime.Driver                { return b.rtDriver }

func (b libkrunDriverBundle) AgentConn(_ context.Context, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("libkrun not available")
}
func (b libkrunDriverBundle) GuestWorkdir(_ string) string { return "/workspace" }
func (b libkrunDriverBundle) DialPort(_ context.Context, _ string, _ int) (net.Conn, error) {
	return nil, fmt.Errorf("libkrun not available")
}
