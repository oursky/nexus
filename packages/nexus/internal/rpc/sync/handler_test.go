package sync

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	appsync "github.com/oursky/nexus/packages/nexus/internal/app/sync"
	domainsync "github.com/oursky/nexus/packages/nexus/internal/domain/sync"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

type mockWSLookup struct{}

func (m *mockWSLookup) Get(_ context.Context, id string) (*domainws.Workspace, error) {
	return &domainws.Workspace{
		ID:       id,
		GuestIP:  "127.0.0.1:2222",
		RootPath: "/workspace",
	}, nil
}

func testLocalPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nexus-sync-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// setup creates a Handler backed by a real Service + MemoryRepository,
// and a MapRegistry with all sync methods registered.
func setup(t *testing.T) (*Handler, *registry.MapRegistry, *domainsync.MemoryRepository) {
	t.Helper()
	repo := domainsync.NewMemoryRepository()
	svc := appsync.NewService(nil, repo, &mockWSLookup{}) // nil mutagen = no real sync, just state tracking
	h := NewHandler(svc)
	reg := registry.NewMapRegistry()
	h.Register(reg)
	return h, reg, repo
}

func dispatch(t *testing.T, reg *registry.MapRegistry, method string, payload any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return reg.Dispatch(context.Background(), method, raw)
}

// ─── workspace.sync-start ────────────────────────────────────────────────

func TestSyncStart_Success(t *testing.T) {
	_, reg, _ := setup(t)

	result, err := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
		"direction":   "bidirectional",
	})
	if err != nil {
		t.Fatalf("sync-start: %v", err)
	}

	resp := result.(SyncStartResponse)
	if resp.SessionID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if resp.Status != "active" {
		t.Fatalf("expected status=active, got %s", resp.Status)
	}
}

func TestSyncStart_DefaultDirection(t *testing.T) {
	_, reg, _ := setup(t)

	result, err := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
	})
	if err != nil {
		t.Fatalf("sync-start: %v", err)
	}

	resp := result.(SyncStartResponse)
	if resp.SessionID == "" {
		t.Fatal("expected non-empty session ID")
	}
}

func TestSyncStart_MissingWorkspaceID(t *testing.T) {
	_, reg, _ := setup(t)

	_, err := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"localPath": testLocalPath(t),
	})
	if err == nil {
		t.Fatal("expected error for missing workspaceId")
	}
}

func TestSyncStart_MissingLocalPath(t *testing.T) {
	_, reg, _ := setup(t)

	_, err := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
	})
	if err == nil {
		t.Fatal("expected error for missing localPath")
	}
}

func TestSyncStart_InvalidDirection(t *testing.T) {
	_, reg, _ := setup(t)

	_, err := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
		"direction":   "sideways",
	})
	if err == nil {
		t.Fatal("expected error for invalid direction")
	}
}

// ─── workspace.sync-stop ────────────────────────────────────────────────

func TestSyncStop_BySessionID(t *testing.T) {
	_, reg, _ := setup(t)

	startResult, _ := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
		"direction":   "bidirectional",
	})
	sessionID := startResult.(SyncStartResponse).SessionID

	stopResult, err := dispatch(t, reg, "workspace.sync-stop", map[string]any{
		"sessionId": sessionID,
	})
	if err != nil {
		t.Fatalf("sync-stop: %v", err)
	}

	stopResp := stopResult.(SyncStopResponse)
	if !stopResp.Success {
		t.Fatal("expected success=true")
	}
}

func TestSyncStop_ByWorkspaceID(t *testing.T) {
	_, reg, _ := setup(t)

	dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
	})

	stopResult, err := dispatch(t, reg, "workspace.sync-stop", map[string]any{
		"workspaceId": "ws-1",
	})
	if err != nil {
		t.Fatalf("sync-stop: %v", err)
	}
	if !stopResult.(SyncStopResponse).Success {
		t.Fatal("expected success=true")
	}
}

func TestSyncStop_MissingBothIDs(t *testing.T) {
	_, reg, _ := setup(t)

	_, err := dispatch(t, reg, "workspace.sync-stop", map[string]any{})
	if err == nil {
		t.Fatal("expected error when both sessionId and workspaceId missing")
	}
}

func TestSyncStop_NonexistentSession(t *testing.T) {
	_, reg, _ := setup(t)

	_, err := dispatch(t, reg, "workspace.sync-stop", map[string]any{
		"sessionId": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

// ─── workspace.sync-status ──────────────────────────────────────────────

func TestSyncStatus_BySessionID(t *testing.T) {
	_, reg, _ := setup(t)

	startResult, _ := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
		"direction":   "up",
	})
	sessionID := startResult.(SyncStartResponse).SessionID

	statusResult, err := dispatch(t, reg, "workspace.sync-status", map[string]any{
		"sessionId": sessionID,
	})
	if err != nil {
		t.Fatalf("sync-status: %v", err)
	}

	statusResp := statusResult.(SyncStatusResponse)
	if statusResp.SessionID != sessionID {
		t.Fatalf("expected sessionID=%s, got %s", sessionID, statusResp.SessionID)
	}
	if statusResp.Status != "active" {
		t.Fatalf("expected status=active, got %s", statusResp.Status)
	}
	if statusResp.Direction != "up" {
		t.Fatalf("expected direction=up, got %s", statusResp.Direction)
	}
	if statusResp.WorkspaceID != "ws-1" {
		t.Fatalf("expected workspaceID=ws-1, got %s", statusResp.WorkspaceID)
	}
	if statusResp.StartedAt == "" {
		t.Fatal("expected non-empty startedAt")
	}
}

func TestSyncStatus_AfterStop(t *testing.T) {
	_, reg, _ := setup(t)

	startResult, _ := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
	})
	sessionID := startResult.(SyncStartResponse).SessionID

	dispatch(t, reg, "workspace.sync-stop", map[string]any{
		"sessionId": sessionID,
	})

	statusResult, err := dispatch(t, reg, "workspace.sync-status", map[string]any{
		"sessionId": sessionID,
	})
	if err != nil {
		t.Fatalf("sync-status after stop: %v", err)
	}

	statusResp := statusResult.(SyncStatusResponse)
	if statusResp.Status != "stopped" {
		t.Fatalf("expected status=stopped, got %s", statusResp.Status)
	}
	if statusResp.StoppedAt == "" {
		t.Fatal("expected non-empty stoppedAt")
	}
}

func TestSyncStatus_MissingBothIDs(t *testing.T) {
	_, reg, _ := setup(t)

	_, err := dispatch(t, reg, "workspace.sync-status", map[string]any{})
	if err == nil {
		t.Fatal("expected error when both sessionId and workspaceId missing")
	}
}

// ─── workspace.sync-list ────────────────────────────────────────────────

func TestSyncList_All(t *testing.T) {
	_, reg, _ := setup(t)

	dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
	})
	dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-2",
		"localPath":   testLocalPath(t),
	})

	result, err := dispatch(t, reg, "workspace.sync-list", map[string]any{})
	if err != nil {
		t.Fatalf("sync-list: %v", err)
	}

	resp := result.(SyncListResponse)
	if len(resp.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(resp.Sessions))
	}
}

func TestSyncList_FilterByWorkspace(t *testing.T) {
	_, reg, _ := setup(t)

	dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-1",
		"localPath":   testLocalPath(t),
	})
	dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-2",
		"localPath":   testLocalPath(t),
	})

	result, err := dispatch(t, reg, "workspace.sync-list", map[string]any{
		"workspaceId": "ws-1",
	})
	if err != nil {
		t.Fatalf("sync-list: %v", err)
	}

	resp := result.(SyncListResponse)
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session for ws-1, got %d", len(resp.Sessions))
	}
	for _, s := range resp.Sessions {
		if s.WorkspaceID != "ws-1" {
			t.Fatalf("expected workspaceID=ws-1, got %s", s.WorkspaceID)
		}
	}
}

func TestSyncList_Empty(t *testing.T) {
	_, reg, _ := setup(t)

	result, err := dispatch(t, reg, "workspace.sync-list", map[string]any{})
	if err != nil {
		t.Fatalf("sync-list: %v", err)
	}

	resp := result.(SyncListResponse)
	if len(resp.Sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(resp.Sessions))
	}
}

// ─── Full lifecycle ──────────────────────────────────────────────────────

func TestSyncFullLifecycle(t *testing.T) {
	_, reg, _ := setup(t)

	// Start
	startResult, err := dispatch(t, reg, "workspace.sync-start", map[string]any{
		"workspaceId": "ws-lifecycle",
		"localPath":   testLocalPath(t),
		"direction":   "bidirectional",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	sessionID := startResult.(SyncStartResponse).SessionID

	// Check status
	statusResult, err := dispatch(t, reg, "workspace.sync-status", map[string]any{
		"sessionId": sessionID,
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if statusResult.(SyncStatusResponse).Status != "active" {
		t.Fatalf("expected active, got %s", statusResult.(SyncStatusResponse).Status)
	}

	// List - should contain our session
	listResult, err := dispatch(t, reg, "workspace.sync-list", map[string]any{
		"workspaceId": "ws-lifecycle",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listResult.(SyncListResponse).Sessions) != 1 {
		t.Fatalf("expected 1 session in list, got %d", len(listResult.(SyncListResponse).Sessions))
	}

	// Stop
	stopResult, err := dispatch(t, reg, "workspace.sync-stop", map[string]any{
		"sessionId": sessionID,
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !stopResult.(SyncStopResponse).Success {
		t.Fatal("expected stop success")
	}

	// Verify stopped
	finalStatus, err := dispatch(t, reg, "workspace.sync-status", map[string]any{
		"sessionId": sessionID,
	})
	if err != nil {
		t.Fatalf("final status: %v", err)
	}
	if finalStatus.(SyncStatusResponse).Status != "stopped" {
		t.Fatalf("expected stopped, got %s", finalStatus.(SyncStatusResponse).Status)
	}
}
