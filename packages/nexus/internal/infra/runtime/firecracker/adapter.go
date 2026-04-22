package firecracker

import (
	"context"
	"errors"
	"fmt"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// Adapter wraps FCDriver to satisfy domain/runtime.Driver.
type Adapter struct {
	d *FCDriver
}

// NewAdapter returns an Adapter that wraps the given FCDriver.
func NewAdapter(d *FCDriver) *Adapter {
	return &Adapter{d: d}
}

var _ domainruntime.Driver = (*Adapter)(nil)

func (a *Adapter) Backend() string {
	return a.d.Backend()
}

func (a *Adapter) Create(ctx context.Context, req *domainruntime.CreateRequest) error {
	return a.d.Create(ctx, CreateRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectRoot: req.ProjectRoot,
		Options:     req.Options,
	})
}

func (a *Adapter) Start(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("workspace is required")
	}
	return a.d.EnsureStarted(ctx, ws.ID, firecrackerProjectRoot(ws))
}

func (a *Adapter) Stop(ctx context.Context, ws *workspace.Workspace) error {
	return a.d.Stop(ctx, ws.ID)
}

func (a *Adapter) Pause(ctx context.Context, ws *workspace.Workspace) error {
	return a.d.Pause(ctx, ws.ID)
}

func (a *Adapter) Resume(ctx context.Context, ws *workspace.Workspace) error {
	return a.d.Resume(ctx, ws.ID)
}

func (a *Adapter) Snapshot(ctx context.Context, ws *workspace.Workspace) (*domainruntime.Snapshot, error) {
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
	req := CreateRequest{
		WorkspaceID: ws.ID,
		ProjectRoot: ws.Repo,
		Options:     map[string]string{"lineage_snapshot_id": snap.ID},
	}
	return a.d.Create(ctx, req)
}

func (a *Adapter) Fork(ctx context.Context, parent *workspace.Workspace, child *workspace.Workspace) (string, error) {
	if a.d.manager == nil {
		return "", fmt.Errorf("manager is required for firecracker Fork")
	}
	parentRoot := firecrackerProjectRoot(parent)
	if parentRoot == "" {
		return "", fmt.Errorf("parent workspace %s has no project root", parent.ID)
	}
	// Copy parent's live /workspace ext4 image into the snapshot store.
	// If the parent VM is running it is paused for the duration of the copy
	// to guarantee a consistent point-in-time image; it is resumed on return.
	if err := a.d.manager.ForkWorkspaceImage(ctx, parent.ID, child.ID); err != nil {
		return "", fmt.Errorf("fork workspace image: %w", err)
	}
	// Register the child with the same project root as the parent (same host
	// git repo). Filesystem isolation is provided by the copied ext4 image.
	if err := a.d.ForkWithRoot(ctx, parent.ID, child.ID, parentRoot); err != nil {
		return "", err
	}
	// Return "" — child.Repo stays as parent.Repo; no new host path is needed.
	return "", nil
}

func (a *Adapter) Destroy(ctx context.Context, ws *workspace.Workspace) error {
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

func firecrackerProjectRoot(ws *workspace.Workspace) string {
	if ws == nil {
		return ""
	}
	if ws.Repo != "" {
		return ws.Repo
	}
	return ws.RootPath
}
