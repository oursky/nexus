package project

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	domainproject "github.com/oursky/nexus/packages/nexus/internal/domain/project"
	domainworkspace "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

type fakeProjectRepo struct {
	data map[string]*domainproject.Project
}

func newFakeProjectRepo() *fakeProjectRepo {
	return &fakeProjectRepo{data: map[string]*domainproject.Project{}}
}

func (r *fakeProjectRepo) Create(_ context.Context, p *domainproject.Project) error {
	r.data[p.ID] = p
	return nil
}

func (r *fakeProjectRepo) Get(_ context.Context, id string) (*domainproject.Project, error) {
	p, ok := r.data[id]
	if !ok {
		return nil, domainproject.ErrNotFound
	}
	return p, nil
}

func (r *fakeProjectRepo) List(_ context.Context) ([]*domainproject.Project, error) {
	out := make([]*domainproject.Project, 0, len(r.data))
	for _, p := range r.data {
		out = append(out, p)
	}
	return out, nil
}

func (r *fakeProjectRepo) Update(_ context.Context, p *domainproject.Project) error {
	r.data[p.ID] = p
	return nil
}

func (r *fakeProjectRepo) Delete(_ context.Context, id string) error {
	delete(r.data, id)
	return nil
}

type fakeWorkspaceRepo struct {
	data map[string]*domainworkspace.Workspace
}

func newFakeWorkspaceRepo() *fakeWorkspaceRepo {
	return &fakeWorkspaceRepo{data: map[string]*domainworkspace.Workspace{}}
}

func (r *fakeWorkspaceRepo) Create(_ context.Context, ws *domainworkspace.Workspace) error {
	r.data[ws.ID] = ws
	return nil
}

func (r *fakeWorkspaceRepo) Get(_ context.Context, id string) (*domainworkspace.Workspace, error) {
	ws, ok := r.data[id]
	if !ok {
		return nil, domainworkspace.ErrNotFound
	}
	return ws, nil
}

func (r *fakeWorkspaceRepo) List(_ context.Context) ([]*domainworkspace.Workspace, error) {
	out := make([]*domainworkspace.Workspace, 0, len(r.data))
	for _, ws := range r.data {
		out = append(out, ws)
	}
	return out, nil
}

func (r *fakeWorkspaceRepo) Update(_ context.Context, ws *domainworkspace.Workspace) error {
	r.data[ws.ID] = ws
	return nil
}

func (r *fakeWorkspaceRepo) Delete(_ context.Context, id string) error {
	delete(r.data, id)
	return nil
}

func TestCreate_DedupByRepo(t *testing.T) {
	repo := newFakeProjectRepo()
	h := New(repo)

	raw1 := json.RawMessage(`{"name":"nexus","repoUrl":"https://github.com/oursky/nexus.git"}`)
	res1Any, err := h.create(context.Background(), raw1)
	if err != nil {
		t.Fatalf("create #1: %v", err)
	}
	res1 := res1Any.(*createRes)

	raw2 := json.RawMessage(`{"name":"nexus-renamed","repoUrl":"https://github.com/oursky/nexus"}`)
	res2Any, err := h.create(context.Background(), raw2)
	if err != nil {
		t.Fatalf("create #2: %v", err)
	}
	res2 := res2Any.(*createRes)

	if res1.Project.ID != res2.Project.ID {
		t.Fatalf("expected same project id for same repo; got %s and %s", res1.Project.ID, res2.Project.ID)
	}
	all, _ := repo.List(context.Background())
	if len(all) != 1 {
		t.Fatalf("expected exactly one project row, got %d", len(all))
	}
}

func TestReconcile_FixesWorkspaceProjectRelationAndRemovesDuplicate(t *testing.T) {
	projRepo := newFakeProjectRepo()
	wsRepo := newFakeWorkspaceRepo()
	h := New(projRepo, WithWorkspaceRepo(wsRepo))
	ctx := context.Background()

	now := time.Now().UTC()
	p1 := &domainproject.Project{
		ID:        "proj-old",
		Name:      "nexus",
		RepoURL:   "/home/newman/magic/nexus",
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
	p2 := &domainproject.Project{
		ID:        "proj-dup",
		Name:      "nexus-dup",
		RepoURL:   "/home/newman/magic/nexus",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = projRepo.Create(ctx, p1)
	_ = projRepo.Create(ctx, p2)

	ws := &domainworkspace.Workspace{
		ID:            "ws-1",
		Repo:          "/home/newman/magic/nexus",
		WorkspaceName: "base",
		State:         domainworkspace.StateCreated,
		CreatedAt:     now,
		UpdatedAt:     now,
		// Missing project relation (buggy old data).
		ProjectID: "",
	}
	_ = wsRepo.Create(ctx, ws)

	resAny, err := h.reconcile(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	res := resAny.(*reconcileRes)

	gotWS, _ := wsRepo.Get(ctx, "ws-1")
	if gotWS.ProjectID != "proj-old" {
		t.Fatalf("workspace projectID = %q, want proj-old", gotWS.ProjectID)
	}
	if _, err := projRepo.Get(ctx, "proj-dup"); err == nil {
		t.Fatalf("expected duplicate project proj-dup to be removed")
	}
	if res.UpdatedWorkspaces == 0 {
		t.Fatalf("expected updated workspaces > 0")
	}
	if res.RemovedProjects == 0 {
		t.Fatalf("expected removed projects > 0")
	}
}
