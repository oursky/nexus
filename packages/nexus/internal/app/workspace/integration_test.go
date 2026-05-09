package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/project"
	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/sandbox"
	"github.com/oursky/nexus/packages/nexus/internal/infra/store"
)

// openIntegrationDB creates a real SQLite database for integration tests.
func openIntegrationDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "integration.db"))
	if err != nil {
		t.Fatalf("open integration db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Spec: INV-007, INV-008, INV-009, INV-011
// TestIntegration_CreateAndGetLifecycle tests the full Create→Start→Stop→Remove
// lifecycle using real SQLite persistence.
func TestIntegration_CreateAndGetLifecycle(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)
	svc := NewService(wsStore, projStore, nil, nil, nil, nil, nil, context.Background()) // no runtime driver for integration
	ctx := context.Background()

	ws := createWorkspaceAndVerify(t, svc, wsStore, ctx)
	startAndPollRunning(t, svc, wsStore, ctx, ws.ID)
	stopAndVerify(t, svc, ctx, ws.ID)
	listAndVerifyCount(t, svc, ctx, 1)
	removeAndVerifyGone(t, svc, wsStore, ctx, ws.ID)
}

func createWorkspaceAndVerify(t *testing.T, svc *Service, wsStore *store.WorkspaceStore, ctx context.Context) *workspace.Workspace {
	spec := workspace.CreateSpec{
		Repo:          "https://github.com/example/integration-test",
		Ref:           "main",
		WorkspaceName: "integration-ws",
		AgentProfile:  "default",
		Backend:       "test",
	}
	ws, err := svc.Create(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ws.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	persisted, err := wsStore.Get(ctx, ws.ID)
	if err != nil {
		t.Fatalf("store get: %v", err)
	}
	if persisted.WorkspaceName != "integration-ws" {
		t.Errorf("persisted name: got %q, want %q", persisted.WorkspaceName, "integration-ws")
	}
	if persisted.State != workspace.StateCreated {
		t.Errorf("persisted state: got %q, want %q", persisted.State, workspace.StateCreated)
	}
	return ws
}

func startAndPollRunning(t *testing.T, svc *Service, wsStore *store.WorkspaceStore, ctx context.Context, wsID string) {
	_, err := svc.Start(ctx, wsID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var check *workspace.Workspace
	for i := 0; i < 20; i++ {
		check, _ = wsStore.Get(ctx, wsID)
		if check != nil && check.State == workspace.StateRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if check == nil || check.State != workspace.StateRunning {
		t.Errorf("persisted running state: got %q", check.State)
	}
}

func stopAndVerify(t *testing.T, svc *Service, ctx context.Context, wsID string) {
	stopped, err := svc.Stop(ctx, wsID)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopped.State != workspace.StateStopped {
		t.Errorf("stopped state: got %q", stopped.State)
	}
}

func listAndVerifyCount(t *testing.T, svc *Service, ctx context.Context, want int) {
	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != want {
		t.Errorf("expected %d workspace(s), got %d", want, len(all))
	}
}

func removeAndVerifyGone(t *testing.T, svc *Service, wsStore *store.WorkspaceStore, ctx context.Context, wsID string) {
	err := svc.Remove(ctx, wsID)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	_, err = wsStore.Get(ctx, wsID)
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound after remove, got: %v", err)
	}
}

// TestIntegration_ForkWithRealPersistence tests forking with real SQLite.
func TestIntegration_ForkWithRealPersistence(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)
	svc := NewService(wsStore, projStore, nil, nil, nil, nil, nil, context.Background())
	ctx := context.Background()

	// Create parent
	parent, err := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "https://github.com/example/fork-test",
		Ref:           "main",
		WorkspaceName: "parent-ws",
		AgentProfile:  "default",
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Fork (no need to start parent; Fork only rejects removed workspaces)
	child, err := svc.Fork(ctx, parent.ID, ForkSpec{
		ChildWorkspaceName: "child-ws",
		ChildRef:           "feature",
	})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}

	if child.ParentWorkspaceID != parent.ID {
		t.Errorf("parent ID: got %q, want %q", child.ParentWorkspaceID, parent.ID)
	}
	if child.LineageRootID != parent.ID {
		t.Errorf("lineage root: got %q, want %q", child.LineageRootID, parent.ID)
	}

	// Verify both exist in store
	all, _ := svc.List(ctx)
	if len(all) != 2 {
		t.Errorf("expected 2 workspaces, got %d", len(all))
	}

	// Verify child persisted correctly
	got, err := wsStore.Get(ctx, child.ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.ParentWorkspaceID != parent.ID {
		t.Errorf("persisted parent: got %q", got.ParentWorkspaceID)
	}
}

// TestIntegration_PortsPersistence tests port operations with real SQLite.
func TestIntegration_PortsPersistence(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)
	svc := NewService(wsStore, projStore, nil, nil, nil, nil, nil, context.Background())
	ctx := context.Background()

	ws, _ := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "https://github.com/example/port-test",
		WorkspaceName: "port-ws",
	})

	// Add ports
	ports, err := svc.PortsAdd(ctx, ws.ID, 8080)
	if err != nil {
		t.Fatalf("ports add: %v", err)
	}
	if len(ports) != 1 {
		t.Errorf("expected 1 port, got %d", len(ports))
	}

	// Verify persisted
	got, _ := wsStore.Get(ctx, ws.ID)
	if len(got.TunnelPorts) != 1 || got.TunnelPorts[0] != 8080 {
		t.Errorf("persisted ports: %v", got.TunnelPorts)
	}

	// Add another
	_, _ = svc.PortsAdd(ctx, ws.ID, 3000)
	got, _ = wsStore.Get(ctx, ws.ID)
	if len(got.TunnelPorts) != 2 {
		t.Errorf("expected 2 ports, got %d: %v", len(got.TunnelPorts), got.TunnelPorts)
	}

	// Remove
	_, _ = svc.PortsRemove(ctx, ws.ID, 8080)
	got, _ = wsStore.Get(ctx, ws.ID)
	if len(got.TunnelPorts) != 1 || got.TunnelPorts[0] != 3000 {
		t.Errorf("after remove: %v", got.TunnelPorts)
	}
}

// TestIntegration_CheckoutPersistence tests checkout with real SQLite.
func TestIntegration_CheckoutPersistence(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)
	svc := NewService(wsStore, projStore, nil, nil, nil, nil, nil, context.Background())
	ctx := context.Background()

	ws, _ := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "https://github.com/example/checkout-test",
		Ref:           "main",
		WorkspaceName: "checkout-ws",
	})

	result, err := svc.Checkout(ctx, ws.ID, CheckoutSpec{TargetRef: "develop"})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if result.Ref != "develop" {
		t.Errorf("ref: got %q, want %q", result.Ref, "develop")
	}

	// Verify persisted
	got, _ := wsStore.Get(ctx, ws.ID)
	if got.Ref != "develop" {
		t.Errorf("persisted ref: got %q", got.Ref)
	}
}

// TestIntegration_WithProject tests workspace creation with a project association.
func TestIntegration_WithProject(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)
	svc := NewService(wsStore, projStore, nil, nil, nil, nil, nil, context.Background())
	ctx := context.Background()

	// Create a project first
	proj := &project.Project{
		ID:      "proj-int-1",
		Name:    "integration-project",
		RepoURL: "https://github.com/example/integration-test",
	}
	if err := projStore.Create(ctx, proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Create workspace with project ID
	ws, err := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "https://github.com/example/integration-test",
		Ref:           "main",
		WorkspaceName: "projected-ws",
		ProjectID:     "proj-int-1",
	})
	if err != nil {
		t.Fatalf("create with project: %v", err)
	}

	// Verify project ID persisted
	got, _ := wsStore.Get(ctx, ws.ID)
	if got.ProjectID != "proj-int-1" {
		t.Errorf("project ID: got %q, want %q", got.ProjectID, "proj-int-1")
	}
}

// TestIntegration_StartWithSandboxDriver tests the Create→Start lifecycle using
// the real sandbox driver and registry. This would have caught the naming
// mismatch where sandbox.Backend() returned "process" but the daemon set the
// default to "sandbox".
func TestIntegration_StartWithSandboxDriver(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)

	registry := domainruntime.NewRegistry()
	sandboxAdapter := sandbox.NewAdapter(sandbox.NewDriver())
	registry.Register(sandboxAdapter)
	registry.SetDefaultBackend("process")

	svc := NewService(wsStore, projStore, registry, nil, nil, nil, nil, context.Background())
	ctx := context.Background()

	ws, err := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "https://github.com/example/sandbox-test",
		Ref:           "main",
		WorkspaceName: "sandbox-ws",
		Backend:       "process",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	var check *workspace.Workspace
	for i := 0; i < 20; i++ {
		check, _ = wsStore.Get(ctx, ws.ID)
		if check != nil && check.State == workspace.StateRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if check == nil || check.State != workspace.StateRunning {
		t.Fatalf("persisted state: got %q, want %q", check.State, workspace.StateRunning)
	}
	if check.Backend != "process" {
		t.Errorf("backend: got %q, want %q", check.Backend, "process")
	}
	if check.RootPath != ws.Repo {
		t.Errorf("rootPath: got %q, want %q", check.RootPath, ws.Repo)
	}
}

// TestIntegration_StartWithMismatchedBackend verifies that even when the
// default backend name is mismatched (the old "sandbox" vs "process" bug),
// the start flow still completes because the driver lookup falls back to nil
// and the service handles nil gracefully.
func TestIntegration_StartWithMismatchedBackend(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)

	registry := domainruntime.NewRegistry()
	sandboxAdapter := sandbox.NewAdapter(sandbox.NewDriver())
	registry.Register(sandboxAdapter)
	// Deliberately set the wrong default — the old bug.
	registry.SetDefaultBackend("sandbox")

	svc := NewService(wsStore, projStore, registry, nil, nil, nil, nil, context.Background())
	ctx := context.Background()

	// Create with empty backend so it resolves to the (wrong) default.
	ws, err := svc.Create(ctx, workspace.CreateSpec{
		Repo:          "https://github.com/example/mismatch-test",
		Ref:           "main",
		WorkspaceName: "mismatch-ws",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	var check *workspace.Workspace
	for i := 0; i < 20; i++ {
		check, _ = wsStore.Get(ctx, ws.ID)
		if check != nil && check.State == workspace.StateRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if check == nil || check.State != workspace.StateRunning {
		t.Fatalf("persisted state: got %q, want %q", check.State, workspace.StateRunning)
	}
}
