package workspace

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/inizio/nexus/packages/nexus/internal/domain/project"
	"github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	"github.com/inizio/nexus/packages/nexus/internal/infra/store"
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

// TestIntegration_CreateAndGetLifecycle tests the full Create→Start→Stop→Remove
// lifecycle using real SQLite persistence.
func TestIntegration_CreateAndGetLifecycle(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)
	svc := NewService(wsStore, projStore, nil) // no runtime driver for integration
	ctx := context.Background()

	// Create
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

	// Verify persisted via direct store read (not through service)
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

	// Start
	started, err := svc.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if started.State != workspace.StateRunning {
		t.Errorf("started state: got %q", started.State)
	}

	// Verify state persisted
	check, _ := wsStore.Get(ctx, ws.ID)
	if check.State != workspace.StateRunning {
		t.Errorf("persisted running state: got %q", check.State)
	}

	// Stop
	stopped, err := svc.Stop(ctx, ws.ID)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopped.State != workspace.StateStopped {
		t.Errorf("stopped state: got %q", stopped.State)
	}

	// List
	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 workspace, got %d", len(all))
	}

	// Remove
	err = svc.Remove(ctx, ws.ID)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Verify gone
	_, err = wsStore.Get(ctx, ws.ID)
	if err != workspace.ErrNotFound {
		t.Errorf("expected ErrNotFound after remove, got: %v", err)
	}
}

// TestIntegration_ForkWithRealPersistence tests forking with real SQLite.
func TestIntegration_ForkWithRealPersistence(t *testing.T) {
	db := openIntegrationDB(t)

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)
	svc := NewService(wsStore, projStore, nil)
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

	// Start parent so it's running
	_, _ = svc.Start(ctx, parent.ID)

	// Fork
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
	svc := NewService(wsStore, projStore, nil)
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
	svc.PortsAdd(ctx, ws.ID, 3000)
	got, _ = wsStore.Get(ctx, ws.ID)
	if len(got.TunnelPorts) != 2 {
		t.Errorf("expected 2 ports, got %d: %v", len(got.TunnelPorts), got.TunnelPorts)
	}

	// Remove
	svc.PortsRemove(ctx, ws.ID, 8080)
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
	svc := NewService(wsStore, projStore, nil)
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
	svc := NewService(wsStore, projStore, nil)
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
