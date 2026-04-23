//go:build linux && libkrun

package daemon

import (
	"context"
	"net"
	"os"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	lkruntime "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
)

func buildLibkrunDriver(cfg Config) (libkrunDriverBundle, error) {
	nexusBin, _ := os.Executable()
	lkCfg := lkruntime.ManagerConfig{
		NexusBin:    nexusBin,
		KernelPath:  cfg.KernelPath,
		RootFSPath:  cfg.RootFSPath,
		WorkDirRoot: cfg.WorkDirRoot,
		BasesDir:    cfg.BasesDir,
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

func (b libkrunDriverBundle) DialPort(ctx context.Context, workspaceID string, port int) (net.Conn, error) {
	return b.adapter.DialPort(ctx, workspaceID, port)
}
