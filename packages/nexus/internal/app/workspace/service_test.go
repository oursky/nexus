package workspace

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	"github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// ── Fakes ────────────────────────────────────────────────────────────────────

// fakeWorkspaceRepo is an in-memory implementation of workspace.Repository.
type fakeWorkspaceRepo struct {
	mu   sync.Mutex
	data map[string]*workspace.Workspace
}

func newFakeWorkspaceRepo() *fakeWorkspaceRepo {
	return &fakeWorkspaceRepo{data: make(map[string]*workspace.Workspace)}
}

func (r *fakeWorkspaceRepo) Create(_ context.Context, ws *workspace.Workspace) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[ws.ID] = ws
	return nil
}

func (r *fakeWorkspaceRepo) Get(_ context.Context, id string) (*workspace.Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ws, ok := r.data[id]
	if !ok {
		return nil, workspace.ErrNotFound
	}
	return ws, nil
}

func (r *fakeWorkspaceRepo) List(_ context.Context) ([]*workspace.Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]*workspace.Workspace, 0, len(r.data))
	for _, ws := range r.data {
		all = append(all, ws)
	}
	return all, nil
}

func (r *fakeWorkspaceRepo) Update(_ context.Context, ws *workspace.Workspace) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[ws.ID]; !ok {
		return workspace.ErrNotFound
	}
	r.data[ws.ID] = ws
	return nil
}

func (r *fakeWorkspaceRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

// fakeProjectRepo is an in-memory project.Repository.
type fakeProjectRepo struct {
	mu   sync.Mutex
	data map[string]*project.Project
}

func newFakeProjectRepo() *fakeProjectRepo {
	return &fakeProjectRepo{data: make(map[string]*project.Project)}
}

func (r *fakeProjectRepo) Create(_ context.Context, p *project.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[p.ID] = p
	return nil
}

func (r *fakeProjectRepo) Get(_ context.Context, id string) (*project.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.data[id]
	if !ok {
		return nil, project.ErrNotFound
	}
	return p, nil
}

func (r *fakeProjectRepo) List(_ context.Context) ([]*project.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]*project.Project, 0, len(r.data))
	for _, p := range r.data {
		all = append(all, p)
	}
	return all, nil
}

func (r *fakeProjectRepo) Update(_ context.Context, p *project.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[p.ID] = p
	return nil
}

func (r *fakeProjectRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

// fakeDriver is an in-memory runtime.Driver that records calls.
type fakeDriver struct {
	mu        sync.Mutex
	creates   []string
	starts    []string
	stops     []string
	destroys  []string
	forks     []string // "parentID->childID"
	restores  []string
	backend   string
	createErr error
	forkErr   error
	ready     *bool
	readyErr  error
}

func (d *fakeDriver) Backend() string { return d.backend }

func (d *fakeDriver) Create(_ context.Context, req *runtime.CreateRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.createErr != nil {
		return d.createErr
	}
	d.creates = append(d.creates, req.WorkspaceID)
	return nil
}

func (d *fakeDriver) Start(_ context.Context, ws *workspace.Workspace) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.starts = append(d.starts, ws.ID)
	return nil
}

func (d *fakeDriver) Stop(_ context.Context, ws *workspace.Workspace) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stops = append(d.stops, ws.ID)
	return nil
}

func (d *fakeDriver) Pause(_ context.Context, ws *workspace.Workspace) error { return nil }

func (d *fakeDriver) Resume(_ context.Context, ws *workspace.Workspace) error { return nil }

func (d *fakeDriver) Snapshot(_ context.Context, ws *workspace.Workspace) (*runtime.Snapshot, error) {
	return &runtime.Snapshot{WorkspaceID: ws.ID}, nil
}

func (d *fakeDriver) Restore(_ context.Context, ws *workspace.Workspace, _ *runtime.Snapshot) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.restores = append(d.restores, ws.ID)
	return nil
}

func (d *fakeDriver) Fork(_ context.Context, parent *workspace.Workspace, child *workspace.Workspace) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.forkErr != nil {
		return "", d.forkErr
	}
	d.forks = append(d.forks, parent.ID+"->"+child.ID)
	return "", nil
}

func (d *fakeDriver) Destroy(_ context.Context, ws *workspace.Workspace) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.destroys = append(d.destroys, ws.ID)
	return nil
}

func (d *fakeDriver) GuestSSHHost(_ context.Context, _ string) (string, bool) {
	return "", false
}

func (d *fakeDriver) WorkspaceReady(_ context.Context, _ *workspace.Workspace) (bool, error) {
	if d.readyErr != nil {
		return false, d.readyErr
	}
	if d.ready == nil {
		return true, nil
	}
	return *d.ready, nil
}

// newTestService creates a Service with fakes and a driver.
func newTestService() (*Service, *fakeWorkspaceRepo, *fakeProjectRepo, *fakeDriver) {
	wr := newFakeWorkspaceRepo()
	pr := newFakeProjectRepo()
	d := &fakeDriver{backend: "test"}
	reg := runtime.NewRegistry()
	reg.Register(d)
	svc := NewService(wr, pr, reg, nil, nil, context.Background())
	return svc, wr, pr, d
}

// newTestServiceNoDriver creates a Service with fakes but no driver.
func newTestServiceNoDriver() (*Service, *fakeWorkspaceRepo) {
	wr := newFakeWorkspaceRepo()
	pr := newFakeProjectRepo()
	svc := NewService(wr, pr, nil, nil, nil, context.Background())
	return svc, wr
}

// ── Create tests ─────────────────────────────────────────────────────────────

func TestService_Create_Success(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()

	spec := workspace.CreateSpec{
		Repo:          "https://github.com/example/repo",
		Ref:           "main",
		WorkspaceName: "test-ws",
		AgentProfile:  "default",
		Backend:       "test",
	}

	ws, err := svc.Create(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if ws.ID == "" {
		t.Error("expected non-empty ID")
	}
	if ws.State != workspace.StateCreated {
		t.Errorf("state: got %q, want %q", ws.State, workspace.StateCreated)
	}
	if ws.Repo != spec.Repo {
		t.Errorf("repo: got %q, want %q", ws.Repo, spec.Repo)
	}
	if ws.WorkspaceName != spec.WorkspaceName {
		t.Errorf("name: got %q, want %q", ws.WorkspaceName, spec.WorkspaceName)
	}
	if ws.LineageRootID != ws.ID {
		t.Errorf("lineage root: got %q, want %q", ws.LineageRootID, ws.ID)
	}

	// Verify persisted
	got, err := repo.Get(ctx, ws.ID)
	if err != nil {
		t.Fatalf("repo get: %v", err)
	}
	if got.WorkspaceName != spec.WorkspaceName {
		t.Errorf("persisted name: got %q, want %q", got.WorkspaceName, spec.WorkspaceName)
	}

	// Verify driver was called
	if len(driver.creates) != 1 || driver.creates[0] != ws.ID {
		t.Errorf("driver creates: %v", driver.creates)
	}
}

func TestService_Create_AutoCreatesProjectWhenMissingProjectID(t *testing.T) {
	svc, _, projRepo, _ := newTestService()
	ctx := context.Background()

	ws, err := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "/home/dev/repo-a",
		Ref:           "main",
		WorkspaceName: "repo-a",
		AgentProfile:  "default",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ws.ProjectID == "" {
		t.Fatal("expected projectID to be auto-assigned")
	}
	p, err := projRepo.Get(ctx, ws.ProjectID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if p.RepoURL != "/home/dev/repo-a" {
		t.Fatalf("project repoURL = %q, want %q", p.RepoURL, "/home/dev/repo-a")
	}
}

func TestService_Create_ReusesExistingProjectByRepoWhenProjectIDMissing(t *testing.T) {
	svc, _, projRepo, _ := newTestService()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	existing := &project.Project{
		ID:        "proj-existing",
		Name:      "repo-a",
		RepoURL:   "/home/dev/repo-a",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := projRepo.Create(ctx, existing); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	ws, err := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "/home/dev/repo-a",
		Ref:           "main",
		WorkspaceName: "repo-a-2",
		AgentProfile:  "default",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ws.ProjectID != "proj-existing" {
		t.Fatalf("projectID = %q, want %q", ws.ProjectID, "proj-existing")
	}
}

func TestService_Create_RejectsUnknownProjectID(t *testing.T) {
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	_, err := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "/home/dev/repo-a",
		Ref:           "main",
		WorkspaceName: "repo-a",
		AgentProfile:  "default",
		ProjectID:     "proj-missing",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestService_Create_RejectsProjectRepoMismatch(t *testing.T) {
	svc, _, projRepo, _ := newTestService()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := projRepo.Create(ctx, &project.Project{
		ID:        "proj-a",
		Name:      "repo-a",
		RepoURL:   "/home/dev/repo-a",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	_, err := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "/home/dev/repo-b",
		Ref:           "main",
		WorkspaceName: "repo-b",
		AgentProfile:  "default",
		ProjectID:     "proj-a",
	})
	if err == nil || !strings.Contains(err.Error(), "repo mismatch") {
		t.Fatalf("expected repo mismatch error, got: %v", err)
	}
}

func TestService_Create_NoDriver(t *testing.T) {
	svc, _ := newTestServiceNoDriver()

	ws, err := svc.Create(context.Background(), workspace.CreateSpec{
		Repo:          "https://github.com/example/repo",
		WorkspaceName: "no-driver-ws",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ws.State != workspace.StateCreated {
		t.Errorf("state: got %q, want %q", ws.State, workspace.StateCreated)
	}
}

func TestService_Create_MissingRepo(t *testing.T) {
	svc, _, _, _ := newTestService()

	_, err := svc.Create(context.Background(), workspace.CreateSpec{
		WorkspaceName: "test",
	})
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
}

func TestService_Create_MissingName(t *testing.T) {
	svc, _, _, _ := newTestService()

	_, err := svc.Create(context.Background(), workspace.CreateSpec{
		Repo: "https://github.com/example/repo",
	})
	if err == nil {
		t.Fatal("expected error for missing workspaceName")
	}
}

func TestService_Create_DriverError_RollsBack(t *testing.T) {
	svc, repo, _, driver := newTestService()
	driver.createErr = fmt.Errorf("boom")

	_, err := svc.Create(context.Background(), workspace.CreateSpec{
		Repo:          "https://github.com/example/repo",
		WorkspaceName: "rollback-test",
	})
	if err == nil {
		t.Fatal("expected error from driver")
	}

	// Workspace should be rolled back (deleted from repo)
	all, _ := repo.List(context.Background())
	if len(all) != 0 {
		t.Errorf("expected rollback (empty repo), got %d items", len(all))
	}
}

func TestService_Create_DriverAlreadyExists_Succeeds(t *testing.T) {
	svc, _, _, driver := newTestService()
	driver.createErr = fmt.Errorf("workspace already exists")

	ws, err := svc.Create(context.Background(), workspace.CreateSpec{
		Repo:          "https://github.com/example/repo",
		WorkspaceName: "idempotent-test",
	})
	if err != nil {
		t.Fatalf("create with already-exists should succeed: %v", err)
	}
	if ws.State != workspace.StateCreated {
		t.Errorf("state: got %q, want %q", ws.State, workspace.StateCreated)
	}
}

func TestService_Create_NormalizesRef(t *testing.T) {
	svc, _, _, _ := newTestService()

	ws, err := svc.Create(context.Background(), workspace.CreateSpec{
		Repo:          "https://github.com/example/repo",
		Ref:           "  main  ",
		WorkspaceName: "ref-test",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ws.Ref != "main" {
		t.Errorf("ref: got %q, want %q (trimmed)", ws.Ref, "main")
	}
}

// ── Start tests ──────────────────────────────────────────────────────────────

func TestService_Start_Success(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-start", workspace.StateStopped)

	result, err := svc.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Start is async: returns StateStarting immediately; poll until StateRunning.
	if result.State != workspace.StateStarting && result.State != workspace.StateRunning {
		t.Errorf("state: got %q, want starting or running", result.State)
	}
	var final *workspace.Workspace
	for i := 0; i < 50; i++ {
		final, _ = repo.Get(ctx, ws.ID)
		if final != nil && final.State == workspace.StateRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final == nil || final.State != workspace.StateRunning {
		t.Errorf("persisted state: got %q, want %q", final.State, workspace.StateRunning)
	}
	if len(driver.starts) != 1 || driver.starts[0] != ws.ID {
		t.Errorf("driver starts: %v", driver.starts)
	}
}

func TestService_Start_NotFound(t *testing.T) {
	svc, _, _, _ := newTestService()

	_, err := svc.Start(context.Background(), "nonexistent")
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestService_Start_Removed(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-start-removed", workspace.StateRemoved)

	_, err := svc.Start(ctx, ws.ID)
	if err == nil {
		t.Fatal("expected error starting removed workspace")
	}
}

// ── Stop tests ───────────────────────────────────────────────────────────────

func TestService_Stop_Success(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-stop", workspace.StateRunning)

	result, err := svc.Stop(ctx, ws.ID)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if result.State != workspace.StateStopped {
		t.Errorf("state: got %q, want %q", result.State, workspace.StateStopped)
	}
	if len(driver.stops) != 1 || driver.stops[0] != ws.ID {
		t.Errorf("driver stops: %v", driver.stops)
	}
}

func TestService_Stop_NotFound(t *testing.T) {
	svc, _, _, _ := newTestService()

	_, err := svc.Stop(context.Background(), "nonexistent")
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestService_Stop_Removed(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-stop-removed", workspace.StateRemoved)

	_, err := svc.Stop(ctx, ws.ID)
	if err == nil {
		t.Fatal("expected error stopping removed workspace")
	}
}

// ── Remove tests ─────────────────────────────────────────────────────────────

func TestService_Remove_Success(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-remove", workspace.StateStopped)

	err := svc.Remove(ctx, ws.ID)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Should be gone from repo
	_, err = repo.Get(ctx, ws.ID)
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound after remove, got: %v", err)
	}

	// Driver destroy should have been called
	if len(driver.destroys) != 1 || driver.destroys[0] != ws.ID {
		t.Errorf("driver destroys: %v", driver.destroys)
	}
}

func TestService_Remove_NotFound(t *testing.T) {
	svc, _, _, _ := newTestService()

	err := svc.Remove(context.Background(), "nonexistent")
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// ── Restore tests ────────────────────────────────────────────────────────────

func TestService_Restore_Success(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-restore", workspace.StateStopped)

	result, err := svc.Restore(ctx, ws.ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if result.State != workspace.StateRestored {
		t.Errorf("state: got %q, want %q", result.State, workspace.StateRestored)
	}
	if len(driver.restores) != 1 || driver.restores[0] != ws.ID {
		t.Errorf("driver restores: %v", driver.restores)
	}
}

func TestService_Restore_Removed(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-restore-removed", workspace.StateRemoved)

	_, err := svc.Restore(ctx, ws.ID)
	if err == nil {
		t.Fatal("expected error restoring removed workspace")
	}
}

// ── Info tests ───────────────────────────────────────────────────────────────

func TestService_Info_Success(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	original := createAndStore(t, repo, "ws-info", workspace.StateRunning)

	got, err := svc.Info(ctx, original.ID)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if got.ID != original.ID {
		t.Errorf("ID: got %q, want %q", got.ID, original.ID)
	}
}

func TestService_Info_NotFound(t *testing.T) {
	svc, _, _, _ := newTestService()

	_, err := svc.Info(context.Background(), "nonexistent")
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// ── List tests ───────────────────────────────────────────────────────────────

func TestService_List_Empty(t *testing.T) {
	svc, _, _, _ := newTestService()

	all, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected empty, got %d", len(all))
	}
}

func TestService_List_SortedByCreatedAt(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws1 := createAndStore(t, repo, "ws-1", workspace.StateRunning)
	ws2 := createAndStore(t, repo, "ws-2", workspace.StateRunning)

	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
	if all[0].ID != ws1.ID {
		t.Errorf("first: got %q, want %q", all[0].ID, ws1.ID)
	}
	if all[1].ID != ws2.ID {
		t.Errorf("second: got %q, want %q", all[1].ID, ws2.ID)
	}
}

// ── Ready tests ──────────────────────────────────────────────────────────────

func TestService_Ready_Running(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	createAndStore(t, repo, "ws-ready", workspace.StateRunning)

	ready, err := svc.Ready(ctx, "ws-ready")
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if !ready {
		t.Error("expected true for running workspace")
	}
}

func TestService_Ready_Stopped(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	createAndStore(t, repo, "ws-not-ready", workspace.StateStopped)

	ready, err := svc.Ready(ctx, "ws-not-ready")
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if ready {
		t.Error("expected false for stopped workspace")
	}
}

func TestService_Ready_UsesDriverReadinessCheck(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()
	readyFlag := false
	driver.ready = &readyFlag

	createAndStore(t, repo, "ws-ready-check", workspace.StateRunning)

	ready, err := svc.Ready(ctx, "ws-ready-check")
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if ready {
		t.Fatal("expected false when runtime driver marks workspace not ready")
	}
}

func TestService_Ready_PropagatesDriverReadinessError(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()
	driver.readyErr = fmt.Errorf("probe failed")

	createAndStore(t, repo, "ws-ready-error", workspace.StateRunning)

	_, err := svc.Ready(ctx, "ws-ready-error")
	if err == nil {
		t.Fatal("expected readiness error")
	}
	if got := err.Error(); got != "probe failed" {
		t.Fatalf("unexpected error: %q", got)
	}
}

// ── Fork tests ───────────────────────────────────────────────────────────────

func TestService_Fork_Success(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()

	parent := createAndStore(t, repo, "ws-parent", workspace.StateRunning)

	child, err := svc.Fork(ctx, parent.ID, ForkSpec{
		ChildWorkspaceName: "child-ws",
		ChildRef:           "feature-branch",
	})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}

	if child.ParentWorkspaceID != parent.ID {
		t.Errorf("parent ID: got %q, want %q", child.ParentWorkspaceID, parent.ID)
	}
	if child.Ref != "feature-branch" {
		t.Errorf("ref: got %q, want %q", child.Ref, "feature-branch")
	}
	if child.WorkspaceName != "child-ws" {
		t.Errorf("name: got %q, want %q", child.WorkspaceName, "child-ws")
	}
	if child.State != workspace.StateCreated {
		t.Errorf("state: got %q, want %q", child.State, workspace.StateCreated)
	}
	if child.Repo != parent.Repo {
		t.Errorf("repo: got %q, want %q", child.Repo, parent.Repo)
	}

	// Verify driver fork called
	if len(driver.forks) != 1 {
		t.Errorf("driver forks: %v", driver.forks)
	}

	// Verify child persisted
	got, err := repo.Get(ctx, child.ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.WorkspaceName != "child-ws" {
		t.Errorf("persisted child name: got %q", got.WorkspaceName)
	}
}

func TestService_Fork_AutoName(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	parent := createAndStore(t, repo, "ws-autoname", workspace.StateRunning)

	child, err := svc.Fork(ctx, parent.ID, ForkSpec{ChildRef: "branch"})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if child.WorkspaceName != "ws-autoname-fork" {
		t.Errorf("auto name: got %q, want %q", child.WorkspaceName, "ws-autoname-fork")
	}
}

func TestService_Fork_ParentNotFound(t *testing.T) {
	svc, _, _, _ := newTestService()

	_, err := svc.Fork(context.Background(), "nonexistent", ForkSpec{ChildRef: "branch"})
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestService_Fork_RemovedParent(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	parent := createAndStore(t, repo, "ws-removed-parent", workspace.StateRemoved)

	_, err := svc.Fork(ctx, parent.ID, ForkSpec{ChildRef: "branch"})
	if err == nil {
		t.Fatal("expected error forking removed workspace")
	}
}

func TestService_Fork_InheritsParentRef(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	parent := createAndStore(t, repo, "ws-fork-noref", workspace.StateRunning)

	// Blank ChildRef should inherit the parent's ref rather than returning an error.
	child, err := svc.Fork(ctx, parent.ID, ForkSpec{ChildRef: "  "})
	if err != nil {
		t.Fatalf("unexpected error for empty childRef: %v", err)
	}
	if child.Ref != parent.Ref {
		t.Fatalf("expected child ref %q, got %q", parent.Ref, child.Ref)
	}
}

func TestService_Fork_DriverError_RollsBack(t *testing.T) {
	svc, repo, _, driver := newTestService()
	ctx := context.Background()
	driver.forkErr = fmt.Errorf("driver boom")

	parent := createAndStore(t, repo, "ws-fork-fail", workspace.StateRunning)

	_, err := svc.Fork(ctx, parent.ID, ForkSpec{ChildRef: "branch"})
	if err == nil {
		t.Fatal("expected fork error")
	}

	// Child should be rolled back
	all, _ := repo.List(ctx)
	// Only parent should exist
	for _, ws := range all {
		if ws.ParentWorkspaceID == parent.ID {
			t.Error("child should have been rolled back")
		}
	}
}

// ── Checkout tests ───────────────────────────────────────────────────────────

func TestService_Checkout_Success(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-checkout", workspace.StateRunning)

	result, err := svc.Checkout(ctx, ws.ID, CheckoutSpec{TargetRef: "develop"})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if result.Ref != "develop" {
		t.Errorf("ref: got %q, want %q", result.Ref, "develop")
	}

	// Verify persisted
	got, _ := repo.Get(ctx, ws.ID)
	if got.Ref != "develop" {
		t.Errorf("persisted ref: got %q", got.Ref)
	}
}

func TestService_Checkout_NotFound(t *testing.T) {
	svc, _, _, _ := newTestService()

	_, err := svc.Checkout(context.Background(), "nonexistent", CheckoutSpec{TargetRef: "main"})
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestService_Checkout_Removed(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-checkout-removed", workspace.StateRemoved)

	_, err := svc.Checkout(ctx, ws.ID, CheckoutSpec{TargetRef: "main"})
	if err == nil {
		t.Fatal("expected error for removed workspace")
	}
}

func TestService_Checkout_EmptyRef(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-checkout-empty", workspace.StateRunning)

	_, err := svc.Checkout(ctx, ws.ID, CheckoutSpec{TargetRef: "  "})
	if err == nil {
		t.Fatal("expected error for empty targetRef")
	}
}

// ── Ports tests ──────────────────────────────────────────────────────────────

func TestService_PortsAdd(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-ports", workspace.StateRunning)

	ports, err := svc.PortsAdd(ctx, ws.ID, 8080)
	if err != nil {
		t.Fatalf("ports add: %v", err)
	}
	if len(ports) != 1 || ports[0] != 8080 {
		t.Errorf("ports: %v", ports)
	}

	// Add second port
	ports, err = svc.PortsAdd(ctx, ws.ID, 3000)
	if err != nil {
		t.Fatalf("ports add second: %v", err)
	}
	if len(ports) != 2 {
		t.Errorf("expected 2 ports, got %d", len(ports))
	}
	// Should be sorted
	if ports[0] != 3000 || ports[1] != 8080 {
		t.Errorf("ports not sorted: %v", ports)
	}
}

func TestService_PortsAdd_Duplicate(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-dup", workspace.StateRunning)

	_, _ = svc.PortsAdd(ctx, ws.ID, 8080)
	ports, err := svc.PortsAdd(ctx, ws.ID, 8080)
	if err != nil {
		t.Fatalf("duplicate add: %v", err)
	}
	if len(ports) != 1 {
		t.Errorf("expected 1 port (dedup), got %d", len(ports))
	}
}

func TestService_PortsAdd_InvalidPort(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-badport", workspace.StateRunning)

	_, err := svc.PortsAdd(ctx, ws.ID, 0)
	if err == nil {
		t.Fatal("expected error for port 0")
	}

	_, err = svc.PortsAdd(ctx, ws.ID, 70000)
	if err == nil {
		t.Fatal("expected error for port 70000")
	}
}

func TestService_PortsRemove(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-rmport", workspace.StateRunning)

	_, _ = svc.PortsAdd(ctx, ws.ID, 8080)
	_, _ = svc.PortsAdd(ctx, ws.ID, 3000)

	ports, err := svc.PortsRemove(ctx, ws.ID, 8080)
	if err != nil {
		t.Fatalf("ports remove: %v", err)
	}
	if len(ports) != 1 || ports[0] != 3000 {
		t.Errorf("ports after remove: %v", ports)
	}
}

func TestService_PortsList(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	ws := createAndStore(t, repo, "ws-listports", workspace.StateRunning)
	_, _ = svc.PortsAdd(ctx, ws.ID, 8080)

	ports, err := svc.PortsList(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ports list: %v", err)
	}
	if len(ports) != 1 || ports[0] != 8080 {
		t.Errorf("ports: %v", ports)
	}
}

// ── Relations tests ──────────────────────────────────────────────────────────

func TestService_Relations(t *testing.T) {
	svc, repo, _, _ := newTestService()
	ctx := context.Background()

	createAndStore(t, repo, "ws-rel1", workspace.StateRunning)
	createAndStore(t, repo, "ws-rel2", workspace.StateRunning)

	rels, err := svc.Relations(ctx, "")
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	if len(rels.Groups) == 0 {
		t.Error("expected at least one relation group")
	}
}

// ── Helper ───────────────────────────────────────────────────────────────────

func createAndStore(t *testing.T, repo *fakeWorkspaceRepo, id string, state workspace.State) *workspace.Workspace {
	t.Helper()
	now := time.Now().UTC()
	ws := &workspace.Workspace{
		ID:            id,
		Repo:          "https://github.com/example/repo",
		RepoID:        "example-repo",
		Ref:           "main",
		WorkspaceName: id,
		AgentProfile:  "default",
		State:         state,
		Backend:       "test",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := repo.Create(context.Background(), ws); err != nil {
		t.Fatalf("createAndStore %s: %v", id, err)
	}
	return ws
}
