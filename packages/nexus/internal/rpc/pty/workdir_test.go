package pty

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// fakeWorkspaceRepo implements domainws.Repository for resolveHostWorkDir tests.
type fakeWorkspaceRepo struct {
	w *domainws.Workspace
}

func (r *fakeWorkspaceRepo) Create(context.Context, *domainws.Workspace) error { return nil }
func (r *fakeWorkspaceRepo) List(context.Context) ([]*domainws.Workspace, error) {
	return nil, nil
}
func (r *fakeWorkspaceRepo) Update(context.Context, *domainws.Workspace) error { return nil }
func (r *fakeWorkspaceRepo) Delete(context.Context, string) error                { return nil }
func (r *fakeWorkspaceRepo) Get(ctx context.Context, id string) (*domainws.Workspace, error) {
	if r.w == nil || r.w.ID != id {
		return nil, domainws.ErrNotFound
	}
	return r.w, nil
}

// TestResolveHostWorkDir_VMLogicalRootAcceptsCLIDefault verifies VM-style
// RootPath /workspace matches the CLI default flag.
func TestResolveHostWorkDir_VMLogicalRootAcceptsCLIDefault(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	const wsID = "ws-test"
	ws := &domainws.Workspace{
		ID:       wsID,
		Repo:     repoDir,
		RootPath: "/workspace",
	}

	h := New(nil,
		WithWorkspaceRepo(&fakeWorkspaceRepo{w: ws}),
		WithProjectRepo(nil),
	)

	hostDir, logical, err := h.resolveHostWorkDir(context.Background(), wsID, "/workspace")
	if err != nil {
		t.Fatalf("resolveHostWorkDir: %v", err)
	}
	if filepath.Clean(hostDir) != filepath.Clean(repoDir) {
		t.Fatalf("hostDir: got %q want %q", hostDir, repoDir)
	}
	if logical != "/workspace" {
		t.Fatalf("logical: got %q", logical)
	}
}
