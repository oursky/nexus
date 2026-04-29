// Package project provides JSON-RPC handlers for project management.
package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	rpce "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

// WorkspaceService is the subset of the workspace application service that the
// project handler needs for cascade-delete.  Using an interface keeps this
// package free of a concrete import cycle with appworkspace.
type WorkspaceService interface {
	Stop(ctx context.Context, id string) (*workspace.Workspace, error)
	Remove(ctx context.Context, id string) error
}

// Handler exposes project.* RPC methods.
type Handler struct {
	repo   project.Repository
	wsRepo workspace.Repository
	wsSvc  WorkspaceService
}

// Option configures project RPC handler wiring.
type Option func(*Handler)

// WithWorkspaceRepo enables cross-entity reconcile operations.
func WithWorkspaceRepo(wsRepo workspace.Repository) Option {
	return func(h *Handler) {
		h.wsRepo = wsRepo
	}
}

// WithWorkspaceService enables cascade-delete of workspaces when a project is removed.
func WithWorkspaceService(svc WorkspaceService) Option {
	return func(h *Handler) {
		h.wsSvc = svc
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
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

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

// ── helpers ──────────────────────────────────────────────────────────────────

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	norm := raw
	if len(norm) == 0 || string(norm) == "null" {
		norm = []byte("{}")
	}
	if err := json.Unmarshal(norm, &v); err != nil {
		return v, rpce.InvalidParams("project.invalid_params", "invalid params: "+err.Error())
	}
	return v, nil
}

func mapErr(err error) error {
	if errors.Is(err, project.ErrNotFound) {
		return rpce.NotFound("project.not_found", "project not found")
	}
	return rpce.Internal("project.internal", fmt.Sprintf("internal error: %v", err))
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
		return nil, rpce.InvalidParams("project.invalid_params", "name is required")
	}
	repoURL := project.NormalizeRepoURL(req.RepoURL)
	if repoURL == "" {
		return nil, rpce.InvalidParams("project.invalid_params", "repoUrl is required")
	}

	// Enforce uniqueness by name — but treat same-name+same-repo as idempotent.
	if existing, err := h.findProjectByName(ctx, name); err != nil {
		return nil, mapErr(err)
	} else if existing != nil {
		if project.NormalizeRepoURL(existing.RepoURL) == repoURL {
			// Idempotent: caller is recreating the same project.
			return &createRes{Project: existing}, nil
		}
		return nil, rpce.InvalidParams("project.duplicate_name", fmt.Sprintf("project name %q already exists", name))
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
		return nil, rpce.InvalidParams("project.invalid_params", "id is required")
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
		return nil, rpce.InvalidParams("project.invalid_params", "id is required")
	}

	// Cascade: stop and destroy all workspaces belonging to this project before
	// removing the project record.  The daemon owns all resource knowledge so
	// this must happen here, not in the client.
	if h.wsRepo != nil && h.wsSvc != nil {
		all, err := h.wsRepo.List(ctx)
		if err != nil {
			return nil, rpce.Internal("project.internal", fmt.Sprintf("list workspaces: %v", err))
		}
		for _, ws := range all {
			if ws == nil || ws.ProjectID != req.ID {
				continue
			}
			// Stop first if running (Remove refuses running workspaces).
			if ws.State == workspace.StateRunning || ws.State == workspace.StateRestored {
				if _, err := h.wsSvc.Stop(ctx, ws.ID); err != nil {
					return nil, rpce.Internal("project.internal", fmt.Sprintf("stop workspace %s: %v", ws.ID, err))
				}
			}
			if err := h.wsSvc.Remove(ctx, ws.ID); err != nil {
				return nil, rpce.Internal("project.internal", fmt.Sprintf("remove workspace %s: %v", ws.ID, err))
			}
		}
	}

	if err := h.repo.Delete(ctx, req.ID); err != nil {
		return nil, mapErr(err)
	}
	return &removeRes{Removed: true}, nil
}

func (h *Handler) findProjectByName(ctx context.Context, name string) (*project.Project, error) {
	all, err := h.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if p == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(p.Name), name) {
			return p, nil
		}
	}
	return nil, nil
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
