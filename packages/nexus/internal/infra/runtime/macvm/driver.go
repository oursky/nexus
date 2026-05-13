//go:build darwin

package macvm

import (
	"context"
	"runtime"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

const backendName = "libkrun"

// Driver implements domain/runtime.Driver for macOS using libkrun microVMs.
// Fork, Snapshot, Pause, and Resume are not supported on this platform.
type Driver struct {
	mgr *Manager
}

// NewDriver constructs a macOS libkrun Driver.
func NewDriver(cfg ManagerConfig) *Driver {
	return &Driver{mgr: NewManager(cfg)}
}

func (d *Driver) Backend() string { return backendName }

// FeatureFlags implements domain/runtime.DriverCapabilities.
// Fork and snapshot are not yet supported on macOS.
func (d *Driver) FeatureFlags() []string {
	return []string{}
}

func (d *Driver) Create(ctx context.Context, req *domainruntime.CreateRequest) error {
	return d.mgr.Create(ctx, req)
}

func (d *Driver) Start(ctx context.Context, ws *domainws.Workspace) error {
	return d.mgr.Start(ctx, ws)
}

func (d *Driver) Stop(ctx context.Context, ws *domainws.Workspace) error {
	return d.mgr.Stop(ctx, ws)
}

func (d *Driver) Pause(_ context.Context, _ *domainws.Workspace) error {
	return domainruntime.NotSupportedOnPlatform("pause", runtime.GOOS)
}

func (d *Driver) Resume(_ context.Context, _ *domainws.Workspace) error {
	return domainruntime.NotSupportedOnPlatform("resume", runtime.GOOS)
}

func (d *Driver) Snapshot(_ context.Context, _ *domainws.Workspace) (*domainruntime.Snapshot, error) {
	return nil, domainruntime.NotSupportedOnPlatform("snapshot", runtime.GOOS)
}

func (d *Driver) Restore(_ context.Context, _ *domainws.Workspace, _ *domainruntime.Snapshot) error {
	return domainruntime.NotSupportedOnPlatform("restore", runtime.GOOS)
}

func (d *Driver) Fork(_ context.Context, _ *domainws.Workspace, _ *domainws.Workspace) (string, error) {
	return "", domainruntime.NotSupportedOnPlatform("fork", runtime.GOOS)
}

func (d *Driver) Destroy(ctx context.Context, ws *domainws.Workspace) error {
	return d.mgr.Destroy(ctx, ws)
}

func (d *Driver) GuestSSHHost(ctx context.Context, workspaceID string) (string, bool) {
	return d.mgr.GuestSSHHost(ctx, workspaceID)
}
