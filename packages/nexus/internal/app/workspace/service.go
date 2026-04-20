package workspace

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/inizio/nexus/packages/nexus/internal/domain/project"
	"github.com/inizio/nexus/packages/nexus/internal/domain/runtime"
	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	"github.com/inizio/nexus/packages/nexus/internal/infra/config"
	"github.com/inizio/nexus/packages/nexus/internal/infra/dockercompose"
)

// Service orchestrates workspace lifecycle operations.
type Service struct {
	repo          workspace.Repository
	projectRepo   project.Repository
	driver        runtime.Driver
	portForwarder spotlightPortForwarder
}

// spotlightPortForwarder creates port forwards when a workspace starts.
type spotlightPortForwarder interface {
	StartSpotlight(ctx context.Context, workspaceID string, spec spotlight.ExposeSpec) (*spotlight.Forward, error)
}

// NewService constructs a Service. driver and portForwarder may be nil.
func NewService(repo workspace.Repository, projectRepo project.Repository, driver runtime.Driver) *Service {
	return &Service{repo: repo, projectRepo: projectRepo, driver: driver}
}

// SetPortForwarder injects the spotlight port forwarder. Call after construction.
func (s *Service) SetPortForwarder(pf spotlightPortForwarder) {
	s.portForwarder = pf
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
	return ws, nil
}

func (s *Service) List(ctx context.Context) ([]*workspace.Workspace, error) {
	all, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})
	return all, nil
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

	now := time.Now().UTC()
	id := fmt.Sprintf("ws-%d", now.UnixNano())
	ref := normalizeRef(spec.Ref)

	authBinding := spec.AuthBinding
	if authBinding == nil {
		authBinding = make(map[string]string)
	}

	ws := &workspace.Workspace{
		ID:            id,
		ProjectID:     spec.ProjectID,
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
	}

	return ws, nil
}

func (s *Service) Start(ctx context.Context, id string) (*workspace.Workspace, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.State == workspace.StateRemoved {
		return nil, fmt.Errorf("cannot start removed workspace: %s", id)
	}

	if s.driver != nil {
		if err := s.driver.Start(ctx, ws); err != nil {
			return nil, fmt.Errorf("runtime start: %w", err)
		}
	}

	ws.State = workspace.StateRunning
	ws.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("persist start: %w", err)
	}

	// Auto-forward ports from docker-compose + workspace config
	if s.portForwarder != nil && ws.Repo != "" {
		s.autoForwardPorts(ctx, ws)
	}

	return ws, nil
}

// autoForwardPorts discovers ports from docker-compose and workspace config,
// then creates spotlight forwards for each unique port.
func (s *Service) autoForwardPorts(ctx context.Context, ws *workspace.Workspace) {
	forwards := s.resolveForwardPorts(ctx, ws.Repo)
	for _, fwd := range forwards {
		spec := spotlight.ExposeSpec{
			LocalPort:   fwd.localPort,
			RemotePort:  fwd.remotePort,
			Protocol:    "tcp",
			Source:      spotlight.ForwardSourceAuto,
			WorkspaceID: ws.ID,
		}
		if _, err := s.portForwarder.StartSpotlight(ctx, ws.ID, spec); err != nil {
			log.Printf("workspace.start: auto-forward port %d→%d failed: %v", fwd.localPort, fwd.remotePort, err)
		} else {
			log.Printf("workspace.start: auto-forwarded port %d→%d", fwd.localPort, fwd.remotePort)
		}
	}
}

// forwardPort is a resolved (local, remote) port pair.
type forwardPort struct {
	localPort  int
	remotePort int
}

// resolveForwardPorts merges config defaults with docker-compose port discovery.
// Config defaults take precedence on conflict (same remote port).
func (s *Service) resolveForwardPorts(ctx context.Context, projectRoot string) []forwardPort {
	// 1. Load workspace config defaults
	var configDefaults []forwardPort
	cfg, _, err := config.LoadWorkspaceConfig(projectRoot)
	if err == nil {
		for _, d := range cfg.Spotlight.Defaults {
			if d.RemotePort > 0 && d.LocalPort > 0 {
				configDefaults = append(configDefaults, forwardPort{
					localPort:  d.LocalPort,
					remotePort: d.RemotePort,
				})
			}
		}
	}

	// 2. Discover docker-compose ports
	var composePorts []forwardPort
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if ports, err := dockercompose.DiscoverPublishedPorts(discoverCtx, projectRoot); err == nil {
		for _, p := range ports {
			composePorts = append(composePorts, forwardPort{
				localPort:  p.HostPort,
				remotePort: p.TargetPort,
			})
		}
	}

	// 3. Merge: config defaults override compose ports on same remote port
	remoteToLocal := make(map[int]int) // remotePort → localPort
	// Add compose ports first
	for _, p := range composePorts {
		remoteToLocal[p.remotePort] = p.localPort
	}
	// Config defaults override
	for _, p := range configDefaults {
		remoteToLocal[p.remotePort] = p.localPort
	}

	// 4. Build sorted result
	result := make([]forwardPort, 0, len(remoteToLocal))
	for remote, local := range remoteToLocal {
		result = append(result, forwardPort{localPort: local, remotePort: remote})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].remotePort < result[j].remotePort
	})
	return result
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
	return ws, nil
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
	return ws.State == workspace.StateRunning, nil
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
