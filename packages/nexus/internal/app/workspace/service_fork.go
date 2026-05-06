package workspace

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// ForkSpec describes parameters for forking a workspace.
type ForkSpec struct {
	ChildWorkspaceName string `json:"childWorkspaceName,omitempty"`
	ChildRef           string `json:"childRef"`
}

// CheckoutSpec describes parameters for checking out a new ref.
type CheckoutSpec struct {
	TargetRef string `json:"targetRef"`
}

func (s *Service) Fork(ctx context.Context, parentID string, spec ForkSpec) (*workspace.Workspace, error) {
	parent, err := s.repo.Get(ctx, parentID)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if parent.State == workspace.StateRemoved {
		return nil, fmt.Errorf("cannot fork removed workspace: %s", parentID)
	}

	childRef := spec.ChildRef
	if strings.TrimSpace(childRef) == "" {
		childRef = parent.Ref
	}
	childRef = normalizeRef(childRef, "")

	childName := spec.ChildWorkspaceName
	if childName == "" {
		childName = parent.WorkspaceName + "-fork"
	}

	now := time.Now().UTC()
	childID := fmt.Sprintf("ws-%d", now.UnixNano())

	authBinding := make(map[string]string, len(parent.AuthBinding))
	for k, v := range parent.AuthBinding {
		authBinding[k] = v
	}

	lineageRoot := parent.LineageRootID
	if lineageRoot == "" {
		lineageRoot = parent.ID
	}

	child := &workspace.Workspace{
		ID:                childID,
		ProjectID:         parent.ProjectID,
		RepoID:            parent.RepoID,
		Repo:              parent.Repo,
		Ref:               childRef,
		WorkspaceName:     childName,
		AgentProfile:      parent.AgentProfile,
		Policy:            parent.Policy,
		State:             workspace.StateCreated,
		Backend:           parent.Backend,
		ParentWorkspaceID: parent.ID,
		LineageRootID:     lineageRoot,
		AuthBinding:       authBinding,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.repo.Create(ctx, child); err != nil {
		return nil, fmt.Errorf("persist child workspace: %w", err)
	}

	driver := s.driverFor(parent)
	if driver == nil {
		return child, nil
	}

	// Transition the parent to StateSnapshotting so that concurrent
	// workspace.ready callers know the VM is temporarily stopped and will
	// block (via WaitReady's subscription channel) until we signal running.
	parentWasRunning := parent.State == workspace.StateRunning
	if parentWasRunning {
		parent.State = workspace.StateSnapshotting
		parent.UpdatedAt = time.Now().UTC()
		if updateErr := s.repo.Update(ctx, parent); updateErr != nil {
			log.Printf("[workspace] Fork: persist snapshotting state for %s: %v", parentID, updateErr)
		}
	}

	childRoot, forkErr := driver.Fork(ctx, parent, child)
	if forkErr != nil {
		// Restore parent to running if we moved it to snapshotting.
		if parentWasRunning {
			s.markWorkspaceRunning(ctx, parent)
		}
		_ = s.repo.Delete(ctx, child.ID)
		return nil, fmt.Errorf("runtime fork: %w", forkErr)
	}

	// Persist the child's project root when the backend created a new
	// path (for example process backend git worktree). libkrun fork
	// correctness is volume-based; child.Repo remains metadata.
	if childRoot != "" && childRoot != child.Repo {
		child.Repo = childRoot
		child.UpdatedAt = time.Now().UTC()
		_ = s.repo.Update(ctx, child)
	}

	// The driver has restarted the parent VM in the background. Wait for it
	// to become ready asynchronously so we don't block the fork RPC, then
	// transition it back to StateRunning and notify any waiters.
	if parentWasRunning {
		go s.waitAndMarkParentRunning(parent)
	}

	return child, nil
}

// waitAndMarkParentRunning polls the driver until the parent VM is reachable
// after a fork restart, then marks it StateRunning and notifies WaitReady subscribers.
func (s *Service) waitAndMarkParentRunning(parent *workspace.Workspace) {
	ctx, cancel := context.WithTimeout(s.bgCtx, runStartAsyncTimeout)
	defer cancel()

	driver := s.driverFor(parent)
	checker, ok := driver.(runtimeReadinessDriver)
	if !ok || checker == nil {
		// Driver doesn't support readiness probing — mark running immediately.
		s.markWorkspaceRunning(ctx, parent)
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[workspace] Fork: parent %s did not become ready within timeout; marking running anyway", parent.ID)
			s.markWorkspaceRunning(ctx, parent)
			return
		case <-ticker.C:
			ready, err := checker.WorkspaceReady(ctx, parent)
			if err != nil {
				log.Printf("[workspace] Fork: parent %s readiness probe error: %v; marking running", parent.ID, err)
				s.markWorkspaceRunning(ctx, parent)
				return
			}
			if ready {
				s.markWorkspaceRunning(ctx, parent)
				return
			}
		}
	}
}

func (s *Service) Checkout(ctx context.Context, id string, spec CheckoutSpec) (*workspace.Workspace, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.State == workspace.StateRemoved {
		return nil, fmt.Errorf("cannot checkout removed workspace: %s", id)
	}

	if strings.TrimSpace(spec.TargetRef) == "" {
		return nil, fmt.Errorf("targetRef is required")
	}
	targetRef := normalizeRef(spec.TargetRef, "")

	ws.Ref = targetRef
	ws.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("persist checkout: %w", err)
	}
	return ws, nil
}
