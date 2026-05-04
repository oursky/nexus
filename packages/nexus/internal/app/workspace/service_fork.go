package workspace

import (
	"context"
	"fmt"
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

	if driver := s.driverFor(parent); driver != nil {
		childRoot, err := driver.Fork(ctx, parent, child)
		if err != nil {
			_ = s.repo.Delete(ctx, child.ID)
			return nil, fmt.Errorf("runtime fork: %w", err)
		}
		// Persist the child's project root when the backend created a new
		// path (for example process backend git worktree). libkrun fork
		// correctness is volume-based; child.Repo remains metadata.
		if childRoot != "" && childRoot != child.Repo {
			child.Repo = childRoot
			child.UpdatedAt = time.Now().UTC()
			_ = s.repo.Update(ctx, child)
		}
	}

	return child, nil
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
