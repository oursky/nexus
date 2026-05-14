package workspace

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	"github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

func (s *Service) Create(ctx context.Context, spec workspace.CreateSpec) (*workspace.Workspace, error) {
	if spec.Repo == "" {
		return nil, fmt.Errorf("repo is required")
	}
	spec.Repo = normalizeWorkspaceRepoPath(spec.Repo)
	if spec.WorkspaceName == "" {
		return nil, fmt.Errorf("workspaceName is required")
	}
	if err := spec.Policy.Validate(); err != nil {
		return nil, err
	}

	// Validate requested backend is available on this node.
	if spec.Backend != "" && s.registry != nil && !s.registry.HasBackend(spec.Backend) {
		return nil, fmt.Errorf("runtime backend %q is not available on this node (available: %v)", spec.Backend, s.registry.Backends())
	}

	// Check for duplicate workspace name among non-removed workspaces.
	all, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	for _, ws := range all {
		if ws.WorkspaceName == spec.WorkspaceName && ws.State != workspace.StateRemoved {
			return nil, fmt.Errorf("workspace name %q already exists", spec.WorkspaceName)
		}
	}

	projectID, err := s.resolveProjectIDForCreate(ctx, spec)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	id := fmt.Sprintf("ws-%d", now.UnixNano())
	ref := normalizeRef(spec.Ref, spec.Repo)

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

	if err := s.createWorkspaceRuntime(ctx, ws, spec.ConfigBundle); err != nil {
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, err
	}

	return ws, nil
}

func (s *Service) createWorkspaceRuntime(ctx context.Context, ws *workspace.Workspace, configBundle string) error {
	driver := s.driverFor(ws)
	if driver == nil {
		return nil
	}
	req := &runtime.CreateRequest{
		WorkspaceID:   ws.ID,
		WorkspaceName: ws.WorkspaceName,
		ProjectRoot:   ws.Repo,
		ConfigBundle:  configBundle,
	}
	if err := driver.Create(ctx, req); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("runtime create: %w", err)
	}
	// Stamp backend so the PTY handler knows which session type to use.
	if ws.Backend == "" {
		ws.Backend = driver.Backend()
		ws.UpdatedAt = time.Now().UTC()
		_ = s.repo.Update(ctx, ws)
	}
	return nil
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

func normalizeRef(ref, repoPath string) string {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		return ref
	}
	// Auto-detect the current branch from the repo on disk.
	if repoPath = strings.TrimSpace(repoPath); repoPath != "" {
		out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "HEAD").Output()
		if err == nil {
			if branch := strings.TrimSpace(string(out)); branch != "" {
				return branch
			}
		}
	}
	return "main"
}

func deriveRepoID(repo string) string {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.NewReplacer(":", "/", "@", "", "//", "/").Replace(repo)
	return repo
}

// normalizeWorkspaceRepoPath expands tilde and resolves filesystem repo paths to
// absolute paths on the daemon host. Relative paths depend on the daemon's
// working directory and break VM virtio-fs mounts when mis-resolved.
// Behaviour matches cmd/nexus/commands/workspace/create.normalizeRepoForCreate.
func normalizeWorkspaceRepoPath(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" || looksLikeRemoteGitRepo(repo) {
		return repo
	}
	repo = expandTildeRepoPath(repo)
	if filepath.IsAbs(repo) {
		return filepath.Clean(repo)
	}
	if strings.HasPrefix(repo, "./") || strings.HasPrefix(repo, "../") {
		if abs, err := filepath.Abs(repo); err == nil {
			return filepath.Clean(abs)
		}
		return repo
	}
	if info, err := os.Stat(repo); err == nil && info.IsDir() {
		if abs, absErr := filepath.Abs(repo); absErr == nil {
			return filepath.Clean(abs)
		}
	}
	return repo
}

func expandTildeRepoPath(repo string) string {
	if repo == "~" || strings.HasPrefix(repo, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if repo == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(repo, "~/"))
		}
	}
	return repo
}

func looksLikeRemoteGitRepo(repo string) bool {
	r := strings.TrimSpace(repo)
	if strings.HasPrefix(r, "git@") || strings.HasPrefix(r, "ssh://") {
		return true
	}
	u, err := url.Parse(r)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}
