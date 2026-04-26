// Package project provides JSON-RPC handlers for project management.
package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	rpce "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

// Handler exposes project.* RPC methods.
type Handler struct {
	repo   project.Repository
	wsRepo workspace.Repository
}

// Option configures project RPC handler wiring.
type Option func(*Handler)

// WithWorkspaceRepo enables cross-entity reconcile operations.
func WithWorkspaceRepo(wsRepo workspace.Repository) Option {
	return func(h *Handler) {
		h.wsRepo = wsRepo
	}
}

// New creates a Handler backed by the given repository.
func New(repo project.Repository, opts ...Option) *Handler {
	h := &Handler{repo: repo}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

// Register wires all project.* methods into reg.
func (h *Handler) Register(reg registry.Registry) {
	reg.Register("project.list", h.list)
	reg.Register("project.create", h.create)
	reg.Register("project.get", h.get)
	reg.Register("project.remove", h.remove)
	reg.Register("project.reconcile", h.reconcile)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type listReq struct{}
type listRes struct {
	Projects []*project.Project `json:"projects"`
}

type createReq struct {
	Name    string `json:"name"`
	RepoURL string `json:"repoUrl"`
}
type createRes struct {
	Project *project.Project `json:"project"`
}

type getReq struct {
	ID string `json:"id"`
}
type getRes struct {
	Project *project.Project `json:"project"`
}

type removeReq struct {
	ID string `json:"id"`
}
type removeRes struct {
	Removed bool `json:"removed"`
}

type reconcileReq struct{}

type reconcileRes struct {
	UpdatedWorkspaces int      `json:"updatedWorkspaces"`
	RemovedProjects   int      `json:"removedProjects"`
	CreatedProjects   int      `json:"createdProjects"`
	CanonicalProjects []string `json:"canonicalProjects"`
}

// ── helpers ──────────────────────────────────────────────────────────────────

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	norm := raw
	if len(norm) == 0 || string(norm) == "null" {
		norm = []byte("{}")
	}
	if err := json.Unmarshal(norm, &v); err != nil {
		return v, rpce.InvalidParams("invalid params: " + err.Error())
	}
	return v, nil
}

func mapErr(err error) error {
	if errors.Is(err, project.ErrNotFound) {
		return rpce.NotFound("project not found")
	}
	return rpce.Internal(fmt.Sprintf("internal error: %v", err))
}

// ── handlers ─────────────────────────────────────────────────────────────────

func (h *Handler) list(ctx context.Context, raw json.RawMessage) (any, error) {
	all, err := h.repo.List(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	if all == nil {
		all = []*project.Project{}
	}
	return &listRes{Projects: all}, nil
}

func (h *Handler) create(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[createReq](raw)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, rpce.InvalidParams("name is required")
	}
	repoURL := project.NormalizeRepoURL(req.RepoURL)
	if repoURL == "" {
		return nil, rpce.InvalidParams("repoUrl is required")
	}

	// Enforce uniqueness by repository regardless of requested display name.
	if existing, err := h.findCanonicalProjectByRepo(ctx, repoURL); err != nil {
		return nil, mapErr(err)
	} else if existing != nil {
		return &createRes{Project: existing}, nil
	}

	now := time.Now().UTC()
	p := &project.Project{
		ID:        project.DeriveIDFromRepo(repoURL),
		Name:      name,
		RepoURL:   repoURL,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.repo.Create(ctx, p); err != nil {
		return nil, mapErr(err)
	}
	// Re-fetch so the response reflects the stored record (handles upsert case
	// where the project already existed with an earlier CreatedAt).
	stored, err := h.repo.Get(ctx, p.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &createRes{Project: stored}, nil
}

func (h *Handler) get(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[getReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	p, err := h.repo.Get(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &getRes{Project: p}, nil
}

func (h *Handler) remove(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[removeReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	if err := h.repo.Delete(ctx, req.ID); err != nil {
		return nil, mapErr(err)
	}
	return &removeRes{Removed: true}, nil
}

func (h *Handler) reconcile(ctx context.Context, raw json.RawMessage) (any, error) {
	_, err := decode[reconcileReq](raw)
	if err != nil {
		return nil, err
	}
	if h.wsRepo == nil {
		return nil, rpce.Internal("workspace repository not configured for project.reconcile")
	}

	projects, err := h.repo.List(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	workspaces, err := h.wsRepo.List(ctx)
	if err != nil {
		return nil, rpce.Internal(fmt.Sprintf("list workspaces: %v", err))
	}

	byRepo := make(map[string][]*project.Project)
	byID := make(map[string]*project.Project)
	for _, p := range projects {
		if p == nil {
			continue
		}
		byID[p.ID] = p
		key := project.NormalizeRepoURL(p.RepoURL)
		if key == "" {
			continue
		}
		byRepo[key] = append(byRepo[key], p)
	}

	canonicalByRepo := make(map[string]*project.Project)
	for repoKey, list := range byRepo {
		slices.SortFunc(list, func(a, b *project.Project) int {
			if a.CreatedAt.Before(b.CreatedAt) {
				return -1
			}
			if a.CreatedAt.After(b.CreatedAt) {
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		})
		canonicalByRepo[repoKey] = list[0]
	}

	createdProjects := 0
	updatedWorkspaces := 0
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		repoKey := project.NormalizeRepoURL(ws.Repo)
		if repoKey == "" {
			continue
		}
		canonical := canonicalByRepo[repoKey]
		if canonical == nil {
			now := time.Now().UTC()
			p := &project.Project{
				ID:        project.DeriveIDFromRepo(repoKey),
				Name:      project.InferNameFromRepo(ws.Repo),
				RepoURL:   ws.Repo,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := h.repo.Create(ctx, p); err != nil {
				return nil, rpce.Internal(fmt.Sprintf("create canonical project for repo %q: %v", repoKey, err))
			}
			createdProjects++
			canonical = p
			canonicalByRepo[repoKey] = p
			byID[p.ID] = p
		}
		if ws.ProjectID != canonical.ID {
			ws.ProjectID = canonical.ID
			ws.UpdatedAt = time.Now().UTC()
			if err := h.wsRepo.Update(ctx, ws); err != nil {
				return nil, rpce.Internal(fmt.Sprintf("update workspace %s projectId: %v", ws.ID, err))
			}
			updatedWorkspaces++
		}
	}

	usedProjects := make(map[string]struct{})
	workspaces, err = h.wsRepo.List(ctx)
	if err != nil {
		return nil, rpce.Internal(fmt.Sprintf("list workspaces for usage scan: %v", err))
	}
	for _, ws := range workspaces {
		if ws == nil || strings.TrimSpace(ws.ProjectID) == "" {
			continue
		}
		usedProjects[ws.ProjectID] = struct{}{}
	}

	removedProjects := 0
	for repoKey, list := range byRepo {
		canonical := canonicalByRepo[repoKey]
		for _, p := range list {
			if p == nil || p.ID == canonical.ID {
				continue
			}
			if _, inUse := usedProjects[p.ID]; inUse {
				continue
			}
			if err := h.repo.Delete(ctx, p.ID); err != nil {
				return nil, rpce.Internal(fmt.Sprintf("remove duplicate project %s: %v", p.ID, err))
			}
			removedProjects++
		}
	}

	canonicalIDs := make([]string, 0, len(canonicalByRepo))
	for _, p := range canonicalByRepo {
		canonicalIDs = append(canonicalIDs, p.ID)
	}
	slices.Sort(canonicalIDs)

	return &reconcileRes{
		UpdatedWorkspaces: updatedWorkspaces,
		RemovedProjects:   removedProjects,
		CreatedProjects:   createdProjects,
		CanonicalProjects: canonicalIDs,
	}, nil
}

func (h *Handler) findCanonicalProjectByRepo(ctx context.Context, repoURL string) (*project.Project, error) {
	all, err := h.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	var best *project.Project
	target := project.NormalizeRepoURL(repoURL)
	for _, p := range all {
		if p == nil {
			continue
		}
		if project.NormalizeRepoURL(p.RepoURL) != target {
			continue
		}
		if best == nil || p.CreatedAt.Before(best.CreatedAt) || (p.CreatedAt.Equal(best.CreatedAt) && p.ID < best.ID) {
			best = p
		}
	}
	return best, nil
}
