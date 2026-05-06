package workspace

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	"github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// RegistryResolver resolves the correct runtime driver for a workspace.
type RegistryResolver interface {
	Driver(backend string) (runtime.Driver, bool)
	DefaultBackend() string
	HasBackend(backend string) bool
	Backends() []string
}

// ForwardCreator creates port forwards for a workspace.
type ForwardCreator interface {
	StartSpotlight(ctx context.Context, workspaceID string, spec spotlight.ExposeSpec) (*spotlight.Forward, error)
}

// Service orchestrates workspace lifecycle operations.
type Service struct {
	repo           workspace.Repository
	projectRepo    project.Repository
	registry       RegistryResolver
	portDiscoverer workspace.PortDiscoverer
	forwardCreator ForwardCreator
	// bgCtx is a long-lived context used for async workspace starts so they
	// are not cancelled when the originating RPC request context expires.
	bgCtx context.Context

	// readyMu guards readySubs.
	readyMu sync.Mutex
	// readySubs holds pending channels waiting for a workspace to become running.
	// Each channel is closed (broadcast) when markWorkspaceRunning fires.
	readySubs map[string][]chan struct{}
}

type runtimeReadinessDriver interface {
	WorkspaceReady(ctx context.Context, ws *workspace.Workspace) (bool, error)
}

// NewService constructs a Service. registry may be nil if no runtime backend is available.
// bgCtx should be the daemon's root context (cancelled only on shutdown).
func NewService(repo workspace.Repository, projectRepo project.Repository, registry RegistryResolver, portDiscoverer workspace.PortDiscoverer, forwardCreator ForwardCreator, bgCtx context.Context) *Service {
	return &Service{repo: repo, projectRepo: projectRepo, registry: registry, portDiscoverer: portDiscoverer, forwardCreator: forwardCreator, bgCtx: bgCtx, readySubs: make(map[string][]chan struct{})}
}

// driverFor returns the runtime driver for a workspace, or nil if none is available.
func (s *Service) driverFor(ws *workspace.Workspace) runtime.Driver {
	if s.registry == nil {
		return nil
	}
	d, _ := s.registry.Driver(ws.Backend)
	return d
}

// Relations is a graph of workspaces grouped by repo.
type Relations struct {
	Groups []RelationGroup `json:"groups"`
}

// RelationGroup is a set of workspaces sharing a repo.
type RelationGroup struct {
	RepoID       string         `json:"repoId"`
	Repo         string         `json:"repo"`
	Nodes        []RelationNode `json:"nodes"`
	LineageRoots []string       `json:"lineageRoots"`
}

// RelationNode is a slim workspace view for relation graphs.
type RelationNode struct {
	WorkspaceID       string          `json:"workspaceId"`
	ParentWorkspaceID string          `json:"parentWorkspaceId,omitempty"`
	LineageRootID     string          `json:"lineageRootId,omitempty"`
	WorktreeRef       string          `json:"worktreeRef,omitempty"`
	State             workspace.State `json:"state"`
	Backend           string          `json:"backend,omitempty"`
	WorkspaceName     string          `json:"workspaceName"`
	CreatedAt         string          `json:"createdAt"`
	UpdatedAt         string          `json:"updatedAt"`
}

func (s *Service) Info(ctx context.Context, id string) (*workspace.Workspace, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	return s.cloneWithGuestIP(ctx, ws), nil
}

func (s *Service) List(ctx context.Context) ([]*workspace.Workspace, error) {
	all, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID < all[j].ID
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})
	out := make([]*workspace.Workspace, len(all))
	for i, ws := range all {
		out[i] = s.cloneWithGuestIP(ctx, ws)
	}
	return out, nil
}

// Start transitions a workspace to StateStarting immediately and boots the VM
// in the background. The caller should poll workspace.info until state==running.
// If the workspace is already starting or running, it returns the current state.
func (s *Service) Start(ctx context.Context, id string) (*workspace.Workspace, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.State == workspace.StateRemoved {
		return nil, fmt.Errorf("cannot start removed workspace: %s", id)
	}
	// Idempotent: if already starting or running, return current state.
	if ws.State == workspace.StateStarting || ws.State == workspace.StateRunning {
		return s.cloneWithGuestIP(ctx, ws), nil
	}

	// Transition to starting immediately so the caller can return to the client.
	ws.State = workspace.StateStarting
	ws.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("persist starting: %w", err)
	}

	wsCopy := *ws
	go s.runStartAsync(&wsCopy)

	return s.cloneWithGuestIP(ctx, ws), nil
}

func (s *Service) Stop(ctx context.Context, id string) (*workspace.Workspace, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.State == workspace.StateRemoved {
		return nil, fmt.Errorf("cannot stop removed workspace: %s", id)
	}
	// WS-027 / INV-013: stop is only valid from a running (or paused/starting) state.
	if ws.State != workspace.StateRunning && ws.State != workspace.StatePaused && ws.State != workspace.StateStarting {
		return nil, fmt.Errorf("cannot stop workspace in state %s: %s", ws.State, id)
	}

	if driver := s.driverFor(ws); driver != nil {
		if err := driver.Stop(ctx, ws); err != nil {
			return nil, fmt.Errorf("runtime stop: %w", err)
		}
	}

	ws.State = workspace.StateStopped
	ws.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("persist stop: %w", err)
	}
	return s.cloneWithGuestIP(ctx, ws), nil
}

func (s *Service) Remove(ctx context.Context, id string) error {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return workspace.ErrNotFound
	}
	if ws.State == workspace.StateRunning {
		return fmt.Errorf("cannot remove running workspace: %s", id)
	}

	if driver := s.driverFor(ws); driver != nil {
		if err := driver.Destroy(ctx, ws); err != nil {
			_ = err
		}
	}

	return s.repo.Delete(ctx, id)
}

func (s *Service) Restore(ctx context.Context, id string) (*workspace.Workspace, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.State == workspace.StateRemoved {
		return nil, fmt.Errorf("cannot restore removed workspace: %s", id)
	}

	if driver := s.driverFor(ws); driver != nil {
		snap := &runtime.Snapshot{WorkspaceID: ws.ID}
		if err := driver.Restore(ctx, ws, snap); err != nil {
			return nil, fmt.Errorf("runtime restore: %w", err)
		}
	}

	ws.State = workspace.StateRestored
	ws.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("persist restore: %w", err)
	}
	return ws, nil
}

func (s *Service) Ready(ctx context.Context, id string) (bool, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return false, workspace.ErrNotFound
	}
	if ws.State != workspace.StateRunning {
		return false, nil
	}
	driver := s.driverFor(ws)
	checker, ok := driver.(runtimeReadinessDriver)
	if !ok || checker == nil {
		return true, nil
	}
	return checker.WorkspaceReady(ctx, ws)
}

// subscribeReady registers a channel that will be closed when the workspace
// transitions to running. The caller must call the returned cancel func to
// clean up if it stops waiting before the notification arrives.
func (s *Service) subscribeReady(id string) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	s.readyMu.Lock()
	s.readySubs[id] = append(s.readySubs[id], ch)
	s.readyMu.Unlock()
	cancel := func() {
		s.readyMu.Lock()
		defer s.readyMu.Unlock()
		subs := s.readySubs[id]
		for i, c := range subs {
			if c == ch {
				s.readySubs[id] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
	}
	return ch, cancel
}

// notifyReady closes all pending subscriber channels for a workspace,
// waking any callers blocked in WaitReady.
func (s *Service) notifyReady(id string) {
	s.readyMu.Lock()
	defer s.readyMu.Unlock()
	for _, ch := range s.readySubs[id] {
		close(ch)
	}
	delete(s.readySubs, id)
}

// WaitReady blocks until the workspace is running and the driver reports ready,
// or until ctx is cancelled. It uses a push-based subscription so it wakes
// immediately when markWorkspaceRunning fires instead of polling.
func (s *Service) WaitReady(ctx context.Context, id string) (bool, error) {
	// Subscribe before the first state check to avoid a TOCTOU race where
	// the workspace transitions between our check and subscribe.
	readyCh, cancelSub := s.subscribeReady(id)
	defer cancelSub()

	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return false, workspace.ErrNotFound
	}

	switch ws.State {
	case workspace.StateRunning:
		// Already running — check driver readiness immediately.
		driver := s.driverFor(ws)
		checker, ok := driver.(runtimeReadinessDriver)
		if !ok || checker == nil {
			return true, nil
		}
		return checker.WorkspaceReady(ctx, ws)
	case workspace.StateCreated, workspace.StateStopped, workspace.StateRestored:
		// Not starting and not running — will never become ready without another start.
		return false, nil
	}

	// State is StateStarting — block until running or ctx done.
	select {
	case <-ctx.Done():
		return false, nil
	case <-readyCh:
	}

	// Re-fetch after notification to get the authoritative state.
	ws, err = s.repo.Get(ctx, id)
	if err != nil {
		return false, workspace.ErrNotFound
	}
	if ws.State != workspace.StateRunning {
		return false, nil
	}
	driver := s.driverFor(ws)
	checker, ok := driver.(runtimeReadinessDriver)
	if !ok || checker == nil {
		return true, nil
	}
	return checker.WorkspaceReady(ctx, ws)
}

func (s *Service) Relations(ctx context.Context, id string) (*Relations, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	groups := make(map[string]*RelationGroup)
	for _, ws := range all {
		repoID := ws.RepoID
		if repoID == "" {
			repoID = "repo-unknown"
		}
		if id != "" && ws.RepoID != id {
			continue
		}
		g, ok := groups[repoID]
		if !ok {
			g = &RelationGroup{RepoID: repoID, Repo: ws.Repo}
			groups[repoID] = g
		}
		g.Nodes = append(g.Nodes, RelationNode{
			WorkspaceID:       ws.ID,
			ParentWorkspaceID: ws.ParentWorkspaceID,
			LineageRootID:     ws.LineageRootID,
			WorktreeRef:       ws.Ref,
			State:             ws.State,
			Backend:           ws.Backend,
			WorkspaceName:     ws.WorkspaceName,
			CreatedAt:         ws.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:         ws.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}

	result := &Relations{}
	for _, g := range groups {
		roots := make(map[string]struct{})
		for _, n := range g.Nodes {
			if n.LineageRootID != "" {
				roots[n.LineageRootID] = struct{}{}
			} else {
				roots[n.WorkspaceID] = struct{}{}
			}
		}
		for rootID := range roots {
			g.LineageRoots = append(g.LineageRoots, rootID)
		}
		sort.Strings(g.LineageRoots)
		result.Groups = append(result.Groups, *g)
	}
	sort.Slice(result.Groups, func(i, j int) bool {
		return result.Groups[i].RepoID < result.Groups[j].RepoID
	})
	return result, nil
}
