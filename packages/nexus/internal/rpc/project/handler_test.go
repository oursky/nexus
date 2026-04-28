package project

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	domainproject "github.com/oursky/nexus/packages/nexus/internal/domain/project"
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

func TestCreate_RejectDuplicateName(t *testing.T) {
	repo := newFakeProjectRepo()
	h := New(repo)

	raw1 := json.RawMessage(`{"name":"duplicate-name","repoUrl":"https://github.com/oursky/first.git"}`)
	_, err := h.create(context.Background(), raw1)
	if err != nil {
		t.Fatalf("create #1: %v", err)
	}

	raw2 := json.RawMessage(`{"name":"duplicate-name","repoUrl":"https://github.com/oursky/second.git"}`)
	_, err = h.create(context.Background(), raw2)
	if err == nil {
		t.Fatal("create #2: expected error for duplicate name, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create #2: expected 'already exists' error, got: %v", err)
	}

	all, _ := repo.List(context.Background())
	if len(all) != 1 {
		t.Fatalf("expected exactly one project row, got %d", len(all))
	}
}
