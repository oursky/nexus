package pty

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	domainproj "github.com/oursky/nexus/packages/nexus/internal/domain/project"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/hostpaths"
	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
)

func workspaceLogicalRoot(ws *domainws.Workspace) string {
	r := strings.TrimSpace(ws.RootPath)
	if r == "" {
		return "/workspace"
	}
	return filepath.Clean(r)
}

// logicalSubpath reports whether requested is logicalRoot or a subdirectory,
// and returns the relative path from logicalRoot to requested (".", "pkg", ...).
func logicalSubpath(logicalRoot, requested string) (rel string, ok bool) {
	lr := filepath.Clean(logicalRoot)
	req := filepath.Clean(requested)
	if lr == "" || req == "" {
		return "", false
	}
	if req == lr {
		return ".", true
	}
	prefix := lr + string(filepath.Separator)
	if strings.HasPrefix(req, prefix) {
		return strings.TrimPrefix(req, prefix), true
	}
	return "", false
}

func isUnderHostRoot(hostRoot, target string) bool {
	rel, err := filepath.Rel(hostRoot, target)
	if err != nil {
		return false
	}
	if rel == ".." {
		return false
	}
	sep := string(filepath.Separator)
	return !strings.HasPrefix(rel, ".."+sep)
}

func validateDir(hostDir, logical string) (string, string, error) {
	fi, err := os.Stat(hostDir)
	if err != nil {
		return "", logical, fmt.Errorf("workDir %q: %w", hostDir, err)
	}
	if !fi.IsDir() {
		return "", logical, rpcerrors.InvalidParams(fmt.Sprintf("workDir %q is not a directory", hostDir))
	}
	return hostDir, logical, nil
}

// resolveWorkspaceHostRoot finds a host directory for PTY cwd. Older workspaces may
// store a Git remote in workspace.repo while the linked project carries the real
// local path in rootPath or repoUrl (folder-picker projects).
func (h *Handler) resolveWorkspaceHostRoot(ctx context.Context, ws *domainws.Workspace) (string, error) {
	candidates := []string{strings.TrimSpace(ws.Repo)}
	if ws.ProjectID != "" && h.proj != nil {
		proj, err := h.proj.Get(ctx, ws.ProjectID)
		if err != nil && !errors.Is(err, domainproj.ErrNotFound) {
			return "", err
		}
		if err == nil {
			if rp := strings.TrimSpace(proj.RootPath); rp != "" {
				candidates = append(candidates, rp)
			}
			if ru := strings.TrimSpace(proj.RepoURL); ru != "" {
				candidates = append(candidates, ru)
			}
		}
	}
	var lastErr error
	seen := make(map[string]bool)
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if hostpaths.IsRemoteGitLocation(c) {
			lastErr = rpcerrors.InvalidParams("remote Git URLs are not supported; workspace and project must use local directory paths that exist on the daemon host")
			continue
		}
		hostRoot, err := hostpaths.ResolveLocalDirOnHost(c)
		if err != nil {
			lastErr = err
			continue
		}
		return hostRoot, nil
	}
	if lastErr == nil {
		return "", rpcerrors.InvalidParams("no local project directory on the daemon for this workspace; use the same absolute path where the project exists on this host")
	}
	return "", lastErr
}

// resolveHostWorkDir maps client workDir (typically the logical /workspace path)
// to a host directory for local PTY processes. Session metadata uses logical paths.
func (h *Handler) resolveHostWorkDir(ctx context.Context, workspaceID, requested string) (hostDir string, logical string, err error) {
	if h.ws == nil {
		return "", "", rpcerrors.Internal("workspace store not configured")
	}
	ws, err := h.ws.Get(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, domainws.ErrNotFound) {
			return "", "", rpcerrors.NotFound("workspace not found")
		}
		return "", "", err
	}
	logicalRoot := workspaceLogicalRoot(ws)
	hostRoot, err := h.resolveWorkspaceHostRoot(ctx, ws)
	if err != nil {
		return "", logicalRoot, err
	}

	req := strings.TrimSpace(requested)
	if req != "" {
		req = filepath.Clean(req)
	}

	// Default + exact logical root → host checkout root.
	if req == "" || req == logicalRoot {
		return hostRoot, logicalRoot, nil
	}

	if rel, ok := logicalSubpath(logicalRoot, req); ok {
		hostJoined := filepath.Join(hostRoot, rel)
		return validateDir(hostJoined, req)
	}

	if filepath.IsAbs(req) {
		if !isUnderHostRoot(hostRoot, req) {
			return "", req, rpcerrors.InvalidParams("workDir must be inside the workspace checkout directory")
		}
		return validateDir(req, req)
	}

	return "", req, rpcerrors.InvalidParams("workDir must be an absolute logical path (/workspace/...) or a path inside the workspace checkout")
}
