package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/hostpaths"
)

// hostRepoTreePath returns a directory on the daemon host for workspace discovery (compose, config).
func (s *Service) hostRepoTreePath(ctx context.Context, ws *workspace.Workspace) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace is nil")
	}
	try := []string{strings.TrimSpace(ws.Repo)}
	if ws.ProjectID != "" && s.projectRepo != nil {
		proj, err := s.projectRepo.Get(ctx, ws.ProjectID)
		if err != nil && !errors.Is(err, project.ErrNotFound) {
			return "", err
		}
		if err == nil && proj != nil {
			try = append(try, strings.TrimSpace(proj.RootPath), strings.TrimSpace(proj.RepoURL))
		}
	}
	var lastErr error
	for _, p := range try {
		if p == "" || hostpaths.IsRemoteGitLocation(p) {
			continue
		}
		resolved, err := hostpaths.ResolveLocalDirOnHost(p)
		if err != nil {
			lastErr = err
			continue
		}
		return resolved, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no host project directory for workspace %s", ws.ID)
}

// materializeHostRepoForFirecracker returns an on-host directory tree used to seed the guest /workspace volume.
func (s *Service) materializeHostRepoForFirecracker(ctx context.Context, spec workspace.CreateSpec, workspaceID string) (string, error) {
	_ = workspaceID
	try := []string{strings.TrimSpace(spec.Repo)}
	if spec.ProjectID != "" && s.projectRepo != nil {
		proj, err := s.projectRepo.Get(ctx, spec.ProjectID)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				return "", fmt.Errorf("%w: project %q not found", workspace.ErrRepoNotOnDaemon, spec.ProjectID)
			}
			return "", err
		}
		if proj != nil {
			try = append(try, strings.TrimSpace(proj.RootPath), strings.TrimSpace(proj.RepoURL))
		}
	}
	var lastErr error
	for _, p := range try {
		if p == "" || hostpaths.IsRemoteGitLocation(p) {
			continue
		}
		resolved, err := hostpaths.ResolveLocalDirOnHost(p)
		if err != nil {
			lastErr = err
			continue
		}
		return resolved, nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("%w: %v", workspace.ErrRepoNotOnDaemon, lastErr)
	}
	return "", fmt.Errorf("%w: no local directory on the daemon for this workspace; use the same absolute path where the project exists on this host", workspace.ErrRepoNotOnDaemon)
}
