//go:build linux

package libkrun

import (
	"context"
	"errors"
	"fmt"
	"net"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// Adapter wraps Driver to satisfy domain/runtime.Driver.
// It mirrors the VM runtime adapter shape used elsewhere in the daemon.
type Adapter struct {
	d *Driver
}

// NewAdapter returns an Adapter wrapping the given Driver.
func NewAdapter(d *Driver) *Adapter {
	return &Adapter{d: d}
}

var _ domainruntime.Driver = (*Adapter)(nil)

func (a *Adapter) Backend() string { return a.d.Backend() }

func (a *Adapter) Create(ctx context.Context, req *domainruntime.CreateRequest) error {
	return a.d.Create(ctx, req.WorkspaceID, req.ProjectRoot, req.Options)
}

func (a *Adapter) Start(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("workspace is required")
	}
	return a.d.EnsureStarted(ctx, ws.ID, libkrunProjectRoot(ws))
}

func (a *Adapter) Stop(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("workspace is required")
	}
	return a.d.Stop(ctx, ws.ID)
}

func (a *Adapter) Pause(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("workspace is required")
	}
	return a.d.Pause(ctx, ws.ID)
}

func (a *Adapter) Resume(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("workspace is required")
	}
	return a.d.Resume(ctx, ws.ID)
}

func (a *Adapter) Snapshot(ctx context.Context, ws *workspace.Workspace) (*domainruntime.Snapshot, error) {
	if ws == nil {
		return nil, errors.New("workspace is required")
	}
	snapshotID, err := a.d.CheckpointFork(ctx, ws.ID, ws.ID+"-snap")
	if err != nil {
		return nil, err
	}
	return &domainruntime.Snapshot{
		ID:          snapshotID,
		WorkspaceID: ws.ID,
	}, nil
}

func (a *Adapter) Restore(ctx context.Context, ws *workspace.Workspace, snap *domainruntime.Snapshot) error {
	if snap == nil {
		return errors.New("snapshot is required for restore")
	}
	return a.d.Create(ctx, ws.ID, ws.Repo, map[string]string{
		"lineage_snapshot_id": snap.ID,
	})
}

func (a *Adapter) Fork(ctx context.Context, parent *workspace.Workspace, child *workspace.Workspace) (string, error) {
	if a.d.manager == nil {
		return "", fmt.Errorf("manager is required for libkrun Fork")
	}
	parentRoot := libkrunProjectRoot(parent)
	if parentRoot == "" {
		return "", fmt.Errorf("parent workspace %s has no project root", parent.ID)
	}
	if err := a.d.manager.ForkWorkspaceImage(ctx, parent.ID, child.ID, parentRoot); err != nil {
		return "", fmt.Errorf("fork workspace image: %w", err)
	}
	if err := a.d.ForkWithRoot(ctx, parent.ID, child.ID, parentRoot); err != nil {
		return "", err
	}
	// Return "" — child.Repo stays as parent.Repo; no new host path is needed.
	return "", nil
}

func (a *Adapter) Destroy(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("workspace is required")
	}
	return a.d.Destroy(ctx, ws.ID)
}

func (a *Adapter) GuestSSHHost(ctx context.Context, workspaceID string) (string, bool) {
	return a.d.GuestSSHHost(ctx, workspaceID)
}

func (a *Adapter) WorkspaceReady(ctx context.Context, ws *workspace.Workspace) (bool, error) {
	if ws == nil {
		return false, errors.New("workspace is required")
	}
	return a.d.WorkspaceReady(ctx, ws.ID)
}

// DialPort satisfies spotlight.PortDialer.
func (a *Adapter) DialPort(ctx context.Context, workspaceID string, remotePort int) (net.Conn, error) {
	return a.d.DialPort(ctx, workspaceID, remotePort)
}

// AgentConn satisfies rpcpty.VsockDialer.
func (a *Adapter) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	return a.d.AgentConn(ctx, workspaceID)
}

// GuestWorkdir satisfies rpcpty.VsockDialer.
func (a *Adapter) GuestWorkdir(workspaceID string) string {
	return a.d.GuestWorkdir(workspaceID)
}

func libkrunProjectRoot(ws *workspace.Workspace) string {
	if ws == nil {
		return ""
	}
	if ws.Repo != "" {
		return ws.Repo
	}
	return ws.RootPath
}
