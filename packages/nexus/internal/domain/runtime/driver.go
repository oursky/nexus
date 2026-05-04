package runtime

import (
	"context"

	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// Driver defines the interface for workspace runtime backends.
type Driver interface {
	Backend() string
	Create(ctx context.Context, req *CreateRequest) error
	Start(ctx context.Context, ws *workspace.Workspace) error
	Stop(ctx context.Context, ws *workspace.Workspace) error
	Pause(ctx context.Context, ws *workspace.Workspace) error
	Resume(ctx context.Context, ws *workspace.Workspace) error
	Snapshot(ctx context.Context, ws *workspace.Workspace) (*Snapshot, error)
	Restore(ctx context.Context, ws *workspace.Workspace, snap *Snapshot) error
	// Fork creates a runtime for the child workspace derived from the parent.
	// It returns the child's project root path (may differ from the parent's
	// when the backend creates a dedicated git worktree), or an empty string
	// if the child should inherit the parent's root. The service persists a
	// non-empty return value as child.Repo so the path survives daemon restarts.
	// Backends may still implement fork correctness via storage snapshots rather
	// than project-root path divergence.
	Fork(ctx context.Context, parent *workspace.Workspace, child *workspace.Workspace) (string, error)
	Destroy(ctx context.Context, ws *workspace.Workspace) error
	// GuestSSHHost returns the guest IPv4 on the engine host bridge when a libkrun
	// workspace VM is running and reachable from the daemon over the tap network.
	GuestSSHHost(ctx context.Context, workspaceID string) (host string, ok bool)
}
