//go:build darwin

package macvm

import (
	"context"
	"errors"
	"net"
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

// WorkspaceReady probes guest tooling readiness (implements workspace.runtimeReadinessDriver).
func (d *Driver) WorkspaceReady(ctx context.Context, ws *domainws.Workspace) (bool, error) {
	if ws == nil {
		return false, errors.New("workspace is required")
	}
	return d.mgr.workspaceReady(ctx, ws.ID)
}

// WorkspaceReadyByID is for daemon components that probe by ID (PTY checker, bundles).
func (d *Driver) WorkspaceReadyByID(ctx context.Context, workspaceID string) (bool, error) {
	return d.mgr.workspaceReady(ctx, workspaceID)
}

// AgentConn connects to the guest exec agent (unix vsock shim or gvproxy-tcp fallback).
func (d *Driver) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	return d.mgr.AgentConn(ctx, workspaceID)
}

// DialPort reaches a guest TCP port via the spotlight vsock mux.
func (d *Driver) DialPort(ctx context.Context, workspaceID string, remotePort int) (net.Conn, error) {
	return d.mgr.DialSpotlight(ctx, workspaceID, remotePort)
}

// SerialLogPath returns a host path hint for VM serial/console logs (may not exist yet).
func (d *Driver) SerialLogPath(workspaceID string) (string, error) {
	return d.mgr.SerialLogPath(workspaceID)
}

// GuestWorkdir is the default working directory for PTY/exec inside the VM.
func (d *Driver) GuestWorkdir(_ string) string { return "/workspace" }
