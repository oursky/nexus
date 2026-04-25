package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// openTestDB creates a fresh in-memory SQLite database with migrations applied.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ── DB initialization tests ──────────────────────────────────────────────────

func TestOpen_CreatesFreshDB(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestOpen_EmptyPath_ReturnsError(t *testing.T) {
	_, err := Open("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// ── WorkspaceStore tests ─────────────────────────────────────────────────────

func newTestWorkspace(id, name string) *workspace.Workspace {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &workspace.Workspace{
		ID:            id,
		ProjectID:     "proj-1",
		Repo:          "https://github.com/example/repo",
		Ref:           "main",
		WorkspaceName: name,
		AgentProfile:  "default",
		State:         workspace.StateStopped,
		RootPath:      "/tmp/ws",
		Backend:       "libkrun",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func TestWorkspaceStore_CreateAndGet(t *testing.T) {
	db := openTestDB(t)
	s := NewWorkspaceStore(db)
	ctx := context.Background()

	original := newTestWorkspace("ws-1", "my-workspace")
	if err := s.Create(ctx, original); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.Get(ctx, "ws-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.ID != original.ID {
		t.Errorf("ID: got %q, want %q", got.ID, original.ID)
	}
	if got.WorkspaceName != original.WorkspaceName {
		t.Errorf("WorkspaceName: got %q, want %q", got.WorkspaceName, original.WorkspaceName)
	}
	if got.State != original.State {
		t.Errorf("State: got %q, want %q", got.State, original.State)
	}
	if got.Repo != original.Repo {
		t.Errorf("Repo: got %q, want %q", got.Repo, original.Repo)
	}
	if got.Backend != original.Backend {
		t.Errorf("Backend: got %q, want %q", got.Backend, original.Backend)
	}
}

func TestWorkspaceStore_Get_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewWorkspaceStore(db)

	_, err := s.Get(context.Background(), "nonexistent")
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestWorkspaceStore_Update(t *testing.T) {
	db := openTestDB(t)
	s := NewWorkspaceStore(db)
	ctx := context.Background()

	ws := newTestWorkspace("ws-2", "update-test")
	if err := s.Create(ctx, ws); err != nil {
		t.Fatalf("create: %v", err)
	}

	ws.State = workspace.StateRunning
	ws.RootPath = "/new/root"
	ws.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)

	if err := s.Update(ctx, ws); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.Get(ctx, "ws-2")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.State != workspace.StateRunning {
		t.Errorf("State after update: got %q, want %q", got.State, workspace.StateRunning)
	}
	if got.RootPath != "/new/root" {
		t.Errorf("RootPath after update: got %q, want %q", got.RootPath, "/new/root")
	}
}

func TestWorkspaceStore_Update_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewWorkspaceStore(db)
	ctx := context.Background()

	ws := newTestWorkspace("ghost", "ghost")
	if err := s.Update(ctx, ws); err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestWorkspaceStore_List_Empty(t *testing.T) {
	db := openTestDB(t)
	s := NewWorkspaceStore(db)

	all, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected empty list, got %d items", len(all))
	}
}

func TestWorkspaceStore_List_Multiple(t *testing.T) {
	db := openTestDB(t)
	s := NewWorkspaceStore(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		ws := newTestWorkspace(
			"ws-list-"+string(rune('a'+i)),
			"workspace-"+string(rune('a'+i)),
		)
		if err := s.Create(ctx, ws); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	all, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 workspaces, got %d", len(all))
	}
}

func TestWorkspaceStore_Delete(t *testing.T) {
	db := openTestDB(t)
	s := NewWorkspaceStore(db)
	ctx := context.Background()

	ws := newTestWorkspace("ws-del", "delete-me")
	if err := s.Create(ctx, ws); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.Delete(ctx, "ws-del"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.Get(ctx, "ws-del")
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestWorkspaceStore_Delete_Idempotent(t *testing.T) {
	db := openTestDB(t)
	s := NewWorkspaceStore(db)

	// Delete non-existent should not error (DELETE with no matching rows is not an error in SQL)
	if err := s.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("delete non-existent: %v", err)
	}
}

// ── ProjectStore tests ───────────────────────────────────────────────────────

func newTestProject(id, name string) *project.Project {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &project.Project{
		ID:        id,
		Name:      name,
		RepoURL:   "https://github.com/example/" + name,
		Config:    project.ProjectConfig{DefaultBackend: "libkrun", DefaultRef: "main"},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestProjectStore_CreateAndGet(t *testing.T) {
	db := openTestDB(t)
	s := NewProjectStore(db)
	ctx := context.Background()

	original := newTestProject("proj-1", "my-project")
	if err := s.Create(ctx, original); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.Get(ctx, "proj-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != original.ID {
		t.Errorf("ID: got %q, want %q", got.ID, original.ID)
	}
	if got.Name != original.Name {
		t.Errorf("Name: got %q, want %q", got.Name, original.Name)
	}
	if got.RepoURL != original.RepoURL {
		t.Errorf("RepoURL: got %q, want %q", got.RepoURL, original.RepoURL)
	}
	if got.Config.DefaultBackend != "libkrun" {
		t.Errorf("Config.DefaultBackend: got %q, want %q", got.Config.DefaultBackend, "libkrun")
	}
}

func TestProjectStore_Get_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewProjectStore(db)

	_, err := s.Get(context.Background(), "nonexistent")
	if err != project.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestProjectStore_Update(t *testing.T) {
	db := openTestDB(t)
	s := NewProjectStore(db)
	ctx := context.Background()

	p := newTestProject("proj-2", "update-test")
	if err := s.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}

	p.Name = "updated-name"
	p.Config.DefaultRef = "develop"
	p.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)

	if err := s.Update(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.Get(ctx, "proj-2")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "updated-name" {
		t.Errorf("Name after update: got %q, want %q", got.Name, "updated-name")
	}
	if got.Config.DefaultRef != "develop" {
		t.Errorf("Config.DefaultRef after update: got %q, want %q", got.Config.DefaultRef, "develop")
	}
}

func TestProjectStore_Update_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewProjectStore(db)

	p := newTestProject("ghost", "ghost")
	if err := s.Update(context.Background(), p); err != project.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestProjectStore_List_Empty(t *testing.T) {
	db := openTestDB(t)
	s := NewProjectStore(db)

	all, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected empty list, got %d items", len(all))
	}
}

func TestProjectStore_List_Multiple(t *testing.T) {
	db := openTestDB(t)
	s := NewProjectStore(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		p := newTestProject(
			"proj-list-"+string(rune('a'+i)),
			"project-"+string(rune('a'+i)),
		)
		if err := s.Create(ctx, p); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	all, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 projects, got %d", len(all))
	}
}

func TestProjectStore_Delete(t *testing.T) {
	db := openTestDB(t)
	s := NewProjectStore(db)
	ctx := context.Background()

	p := newTestProject("proj-del", "delete-me")
	if err := s.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.Delete(ctx, "proj-del"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.Get(ctx, "proj-del")
	if err != project.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

// ── ForwardStore tests ───────────────────────────────────────────────────────

func newTestForward(id, wsID string) *spotlight.Forward {
	return &spotlight.Forward{
		ID:          id,
		WorkspaceID: wsID,
		LocalPort:   8080,
		RemotePort:  80,
		Protocol:    "tcp",
		State:       spotlight.ForwardStateActive,
		CreatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestForwardStore_CreateAndGet(t *testing.T) {
	db := openTestDB(t)
	s := NewForwardStore(db)
	ctx := context.Background()

	original := newTestForward("fwd-1", "ws-1")
	if err := s.Create(ctx, original); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.Get(ctx, "fwd-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != original.ID {
		t.Errorf("ID: got %q, want %q", got.ID, original.ID)
	}
	if got.WorkspaceID != original.WorkspaceID {
		t.Errorf("WorkspaceID: got %q, want %q", got.WorkspaceID, original.WorkspaceID)
	}
	if got.LocalPort != original.LocalPort {
		t.Errorf("LocalPort: got %d, want %d", got.LocalPort, original.LocalPort)
	}
	if got.RemotePort != original.RemotePort {
		t.Errorf("RemotePort: got %d, want %d", got.RemotePort, original.RemotePort)
	}
	if got.State != original.State {
		t.Errorf("State: got %q, want %q", got.State, original.State)
	}
}

func TestForwardStore_Get_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewForwardStore(db)

	_, err := s.Get(context.Background(), "nonexistent")
	if err != spotlight.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestForwardStore_Update(t *testing.T) {
	db := openTestDB(t)
	s := NewForwardStore(db)
	ctx := context.Background()

	fwd := newTestForward("fwd-2", "ws-2")
	if err := s.Create(ctx, fwd); err != nil {
		t.Fatalf("create: %v", err)
	}

	fwd.State = spotlight.ForwardStateInactive
	fwd.LocalPort = 9090

	if err := s.Update(ctx, fwd); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.Get(ctx, "fwd-2")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.State != spotlight.ForwardStateInactive {
		t.Errorf("State after update: got %q, want %q", got.State, spotlight.ForwardStateInactive)
	}
	if got.LocalPort != 9090 {
		t.Errorf("LocalPort after update: got %d, want %d", got.LocalPort, 9090)
	}
}

func TestForwardStore_Update_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewForwardStore(db)

	fwd := newTestForward("ghost", "ghost")
	if err := s.Update(context.Background(), fwd); err != spotlight.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestForwardStore_ListByWorkspace(t *testing.T) {
	db := openTestDB(t)
	s := NewForwardStore(db)
	ctx := context.Background()

	// Create forwards for two different workspaces
	fwd1 := newTestForward("fwd-ws1-a", "ws-1")
	fwd1.LocalPort = 8080
	fwd2 := newTestForward("fwd-ws1-b", "ws-1")
	fwd2.LocalPort = 9090
	fwd3 := newTestForward("fwd-ws2-a", "ws-2")
	fwd3.LocalPort = 3000

	for _, f := range []*spotlight.Forward{fwd1, fwd2, fwd3} {
		if err := s.Create(ctx, f); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// List for ws-1 should return 2 forwards
	ws1Forwards, err := s.ListByWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("list ws-1: %v", err)
	}
	if len(ws1Forwards) != 2 {
		t.Errorf("expected 2 forwards for ws-1, got %d", len(ws1Forwards))
	}

	// List for ws-2 should return 1 forward
	ws2Forwards, err := s.ListByWorkspace(ctx, "ws-2")
	if err != nil {
		t.Fatalf("list ws-2: %v", err)
	}
	if len(ws2Forwards) != 1 {
		t.Errorf("expected 1 forward for ws-2, got %d", len(ws2Forwards))
	}

	// List for non-existent workspace should return empty
	empty, err := s.ListByWorkspace(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("list nonexistent: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 forwards for nonexistent workspace, got %d", len(empty))
	}
}

func TestForwardStore_Delete(t *testing.T) {
	db := openTestDB(t)
	s := NewForwardStore(db)
	ctx := context.Background()

	fwd := newTestForward("fwd-del", "ws-del")
	if err := s.Create(ctx, fwd); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.Delete(ctx, "fwd-del"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.Get(ctx, "fwd-del")
	if err != spotlight.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestForwardStore_Delete_Idempotent(t *testing.T) {
	db := openTestDB(t)
	s := NewForwardStore(db)

	if err := s.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("delete non-existent: %v", err)
	}
}
