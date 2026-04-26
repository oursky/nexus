package workspace

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	"github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/dockercompose"
)

// Service orchestrates workspace lifecycle operations.
type Service struct {
	repo        workspace.Repository
	projectRepo project.Repository
	driver      runtime.Driver
	// bgCtx is a long-lived context used for async workspace starts so they
	// are not cancelled when the originating RPC request context expires.
	bgCtx context.Context
}

type runtimeReadinessDriver interface {
	WorkspaceReady(ctx context.Context, ws *workspace.Workspace) (bool, error)
}

// NewService constructs a Service. driver may be nil if no runtime backend is available.
// bgCtx should be the daemon's root context (cancelled only on shutdown).
func NewService(repo workspace.Repository, projectRepo project.Repository, driver runtime.Driver, bgCtx context.Context) *Service {
	return &Service{repo: repo, projectRepo: projectRepo, driver: driver, bgCtx: bgCtx}
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

// ForkSpec describes parameters for forking a workspace.
type ForkSpec struct {
	ChildWorkspaceName string `json:"childWorkspaceName,omitempty"`
	ChildRef           string `json:"childRef"`
}

// CheckoutSpec describes parameters for checking out a new ref.
type CheckoutSpec struct {
	TargetRef string `json:"targetRef"`
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

func (s *Service) Create(ctx context.Context, spec workspace.CreateSpec) (*workspace.Workspace, error) {
	if spec.Repo == "" {
		return nil, fmt.Errorf("repo is required")
	}
	if spec.WorkspaceName == "" {
		return nil, fmt.Errorf("workspaceName is required")
	}
	if err := spec.Policy.Validate(); err != nil {
		return nil, err
	}
	projectID, err := s.resolveProjectIDForCreate(ctx, spec)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	id := fmt.Sprintf("ws-%d", now.UnixNano())
	ref := normalizeRef(spec.Ref)

	authBinding := spec.AuthBinding
	if authBinding == nil {
		authBinding = make(map[string]string)
	}

	ws := &workspace.Workspace{
		ID:            id,
		ProjectID:     projectID,
		Repo:          spec.Repo,
		RepoID:        deriveRepoID(spec.Repo),
		Ref:           ref,
		WorkspaceName: spec.WorkspaceName,
		AgentProfile:  spec.AgentProfile,
		Policy:        spec.Policy,
		State:         workspace.StateCreated,
		Backend:       spec.Backend,
		AuthBinding:   authBinding,
		ConfigBundle:  spec.ConfigBundle,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	ws.LineageRootID = ws.ID

	if err := s.repo.Create(ctx, ws); err != nil {
		return nil, fmt.Errorf("persist workspace: %w", err)
	}

	if s.driver != nil {
		req := &runtime.CreateRequest{
			WorkspaceID:   ws.ID,
			WorkspaceName: ws.WorkspaceName,
			ProjectRoot:   ws.Repo,
			ConfigBundle:  spec.ConfigBundle,
		}
		if err := s.driver.Create(ctx, req); err != nil && !isAlreadyExists(err) {
			_ = s.repo.Delete(ctx, ws.ID)
			return nil, fmt.Errorf("runtime create: %w", err)
		}
		// Stamp backend so the PTY handler knows which session type to use.
		if ws.Backend == "" {
			ws.Backend = s.driver.Backend()
			ws.UpdatedAt = time.Now().UTC()
			_ = s.repo.Update(ctx, ws)
		}
	}

	return ws, nil
}

func (s *Service) resolveProjectIDForCreate(ctx context.Context, spec workspace.CreateSpec) (string, error) {
	repoKey := project.NormalizeRepoURL(spec.Repo)
	if repoKey == "" {
		return "", fmt.Errorf("repo is required")
	}
	requested := strings.TrimSpace(spec.ProjectID)
	if requested != "" {
		p, err := s.projectRepo.Get(ctx, requested)
		if err != nil {
			return "", fmt.Errorf("projectId %q not found", requested)
		}
		if project.NormalizeRepoURL(p.RepoURL) != repoKey {
			return "", fmt.Errorf("projectId %q repo mismatch", requested)
		}
		return p.ID, nil
	}

	// Backward-compatible create: if no project id is provided, bind to existing
	// project by repo; otherwise create a canonical project record.
	allProjects, err := s.projectRepo.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list projects: %w", err)
	}
	var canonical *project.Project
	for _, p := range allProjects {
		if p == nil {
			continue
		}
		if project.NormalizeRepoURL(p.RepoURL) != repoKey {
			continue
		}
		if canonical == nil || p.CreatedAt.Before(canonical.CreatedAt) || (p.CreatedAt.Equal(canonical.CreatedAt) && p.ID < canonical.ID) {
			canonical = p
		}
	}
	if canonical != nil {
		return canonical.ID, nil
	}

	now := time.Now().UTC()
	newProject := &project.Project{
		ID:        project.DeriveIDFromRepo(spec.Repo),
		Name:      project.InferNameFromRepo(spec.Repo),
		RepoURL:   spec.Repo,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.projectRepo.Create(ctx, newProject); err != nil {
		return "", fmt.Errorf("create inferred project: %w", err)
	}
	return newProject.ID, nil
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

// runStartAsync performs the slow driver.Start work in the background and
// transitions the workspace to running (success) or created (failure).
// If the driver supports WorkspaceReady, it polls until tools are fully
// provisioned before marking the workspace as running (keeping it in the
// starting state during package installation to block premature PTY access).
// runStartAsyncTimeout is the maximum time for the entire start operation including VM spawn.
const runStartAsyncTimeout = 6 * time.Minute

func (s *Service) runStartAsync(ws *workspace.Workspace) {
	id := ws.ID

	// Apply a timeout to the start operation so it doesn't hang forever.
	ctx, cancel := context.WithTimeout(s.bgCtx, runStartAsyncTimeout)
	defer cancel()

	startTime := time.Now()
	log.Printf("[workspace] runStartAsync: start workspace=%s", id)
	defer func() {
		log.Printf("[workspace] runStartAsync: done workspace=%s (%s)", id, time.Since(startTime).Round(time.Millisecond))
	}()

	var startErr error
	if s.driver != nil {
		startErr = s.driver.Start(ctx, ws)
	}

	// Re-fetch latest state in case it was updated externally while we were starting.
	current, fetchErr := s.repo.Get(ctx, id)
	if fetchErr != nil {
		log.Printf("[workspace] runStartAsync: workspace %s disappeared during start: %v", id, fetchErr)
		return
	}

	if startErr != nil {
		log.Printf("[workspace] runStartAsync: start failed for %s: %v", id, startErr)
		// Roll back to created so the user can retry.
		current.State = workspace.StateCreated
		current.UpdatedAt = time.Now().UTC()
		_ = s.repo.Update(ctx, current)
		return
	}

	// If the driver supports readiness probing, keep the workspace in
	// StateStarting until tools are fully provisioned (apt-get installs,
	// opencode/codex npm installs, Docker startup, etc.).  This prevents the
	// Mac app from prematurely showing "running" and keeps PTY sessions blocked
	// until the guest is truly ready.
	if rd, ok := s.driver.(runtimeReadinessDriver); ok {
		wsCopy := *current
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		readinessDeadline := time.Now().Add(3 * time.Minute)
		probeAttempt := 0
		log.Printf("[workspace] runStartAsync: waiting for workspace %s to be ready (tool provisioning)...", id)
		for {
			probeAttempt++
			elapsed := time.Since(readinessDeadline.Add(-3 * time.Minute)).Round(time.Second)
			log.Printf("[workspace] runStartAsync: readiness probe start attempt=%d workspace=%s elapsed=%s", probeAttempt, id, elapsed)

			// Run readiness probe in a goroutine so a hung probe cannot block the loop forever.
			type probeResult struct {
				ready bool
				err   error
			}
			resultCh := make(chan probeResult, 1)
			probeCtx, cancelProbe := context.WithTimeout(ctx, 12*time.Second)
			go func() {
				ready, err := rd.WorkspaceReady(probeCtx, &wsCopy)
				resultCh <- probeResult{ready: ready, err: err}
			}()

			var (
				ready bool
				err   error
			)
			probeStart := time.Now()
			select {
			case <-probeCtx.Done():
				err = probeCtx.Err()
			case result := <-resultCh:
				ready = result.ready
				err = result.err
			}
			cancelProbe()
			probeDur := time.Since(probeStart).Round(time.Millisecond)

			if err == context.DeadlineExceeded {
				log.Printf("[workspace] runStartAsync: readiness probe timeout attempt=%d workspace=%s duration=%s", probeAttempt, id, probeDur)
			} else if err == context.Canceled {
				log.Printf("[workspace] runStartAsync: readiness probe canceled attempt=%d workspace=%s duration=%s", probeAttempt, id, probeDur)
			} else if err != nil {
				log.Printf("[workspace] runStartAsync: readiness probe error attempt=%d workspace=%s duration=%s err=%v", probeAttempt, id, probeDur, err)
			} else {
				log.Printf("[workspace] runStartAsync: readiness probe done attempt=%d workspace=%s duration=%s ready=%t", probeAttempt, id, probeDur, ready)
			}

			if ready {
				log.Printf("[workspace] runStartAsync: workspace %s is ready", id)
				break
			}
			if time.Now().After(readinessDeadline) {
				log.Printf("[workspace] runStartAsync: readiness timeout for %s after %s; proceeding to running", id, 3*time.Minute)
				break
			}
			select {
			case <-ctx.Done():
				log.Printf("[workspace] runStartAsync: context cancelled waiting for %s readiness", id)
				return
			case <-ticker.C:
			}
		}
		// Re-fetch in case state was mutated externally during the wait.
		if cur, err := s.repo.Get(ctx, id); err == nil {
			current = cur
		}
	}

	// Backfill backend for workspaces created before the backend field was stamped.
	if current.Backend == "" && s.driver != nil {
		current.Backend = s.driver.Backend()
	}
	current.State = workspace.StateRunning
	current.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, current); err != nil {
		log.Printf("[workspace] runStartAsync: persist running failed for %s: %v", id, err)
	}
}

// DiscoveredPort is a port discovered from docker-compose or workspace config.
type DiscoveredPort struct {
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
	Service    string `json:"service,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Source     string `json:"source,omitempty"` // "config" or "compose"
}

// DiscoverPorts merges config defaults with docker-compose port discovery.
// Config defaults take precedence on conflict (same remote port).
func (s *Service) DiscoverPorts(ctx context.Context, id string) ([]DiscoveredPort, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.Repo == "" {
		return nil, nil
	}

	// Discover docker-compose ports.
	// HostPort is the port published on the daemon host (e.g., 8080 for "8080:80").
	// Both LocalPort and RemotePort use HostPort since we tunnel to the daemon's published port.
	composeDefaults := make(map[int]DiscoveredPort) // HostPort → port
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if ports, err := dockercompose.DiscoverPublishedPorts(discoverCtx, ws.Repo); err == nil {
		for _, p := range ports {
			composeDefaults[p.HostPort] = DiscoveredPort{
				LocalPort:  p.HostPort,
				RemotePort: p.HostPort,
				Service:    p.Service,
				Protocol:   p.Protocol,
				Source:     "compose",
			}
		}
	}

	merged := composeDefaults

	// 4. Build sorted result
	result := make([]DiscoveredPort, 0, len(merged))
	for _, p := range merged {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].RemotePort < result[j].RemotePort
	})
	return result, nil
}

func (s *Service) Stop(ctx context.Context, id string) (*workspace.Workspace, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.State == workspace.StateRemoved {
		return nil, fmt.Errorf("cannot stop removed workspace: %s", id)
	}

	if s.driver != nil {
		if err := s.driver.Stop(ctx, ws); err != nil {
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

	if s.driver != nil {
		if err := s.driver.Destroy(ctx, ws); err != nil {
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

	if s.driver != nil {
		snap := &runtime.Snapshot{WorkspaceID: ws.ID}
		if err := s.driver.Restore(ctx, ws, snap); err != nil {
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
	checker, ok := s.driver.(runtimeReadinessDriver)
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

func (s *Service) cloneWithGuestIP(ctx context.Context, ws *workspace.Workspace) *workspace.Workspace {
	if ws == nil {
		return nil
	}
	out := *ws
	s.attachGuestIP(ctx, &out)
	return &out
}

func (s *Service) attachGuestIP(ctx context.Context, ws *workspace.Workspace) {
	ws.GuestIP = ""
	if s.driver == nil {
		return
	}
	if ws.State != workspace.StateRunning && ws.State != workspace.StateRestored {
		return
	}
	if !workspace.UsesGuestVM(ws.Backend) {
		return
	}
	ip, ok := s.driver.GuestSSHHost(ctx, ws.ID)
	if ok {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			ws.GuestIP = ip
		}
	}
}

func normalizeRef(ref string) string {
	return strings.TrimSpace(ref)
}

func deriveRepoID(repo string) string {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.NewReplacer(":", "/", "@", "", "//", "/").Replace(repo)
	return repo
}

func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}
