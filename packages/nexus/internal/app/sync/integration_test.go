//go:build integration
// +build integration

package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	domainsync "github.com/oursky/nexus/packages/nexus/internal/domain/sync"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// mockWSLookup is a test double for WorkspaceLookup.
type integrationMockWSLookup struct {
	workspaces map[string]*domainws.Workspace
	betaDirs   map[string]string
}

func newIntegrationMockWSLookup() *integrationMockWSLookup {
	return &integrationMockWSLookup{
		workspaces: map[string]*domainws.Workspace{
			"ws-1": {
				ID:       "ws-1",
				RootPath: "",
				GuestIP:  "local",
				State:    domainws.StateRunning,
			},
			"ws-2": {
				ID:       "ws-2",
				RootPath: "",
				GuestIP:  "local",
				State:    domainws.StateRunning,
			},
		},
		betaDirs: make(map[string]string),
	}
}

func (m *integrationMockWSLookup) Get(_ context.Context, id string) (*domainws.Workspace, error) {
	ws, ok := m.workspaces[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return ws, nil
}

// getBetaDir returns a local directory to use as beta for integration tests.
func (m *integrationMockWSLookup) getBetaDir(t *testing.T, workspaceID string) string {
	if dir, ok := m.betaDirs[workspaceID]; ok {
		return dir
	}
	dir := t.TempDir()
	m.betaDirs[workspaceID] = dir
	return dir
}

// TestEmbeddedMutagenClient_Lifecycle tests the full lifecycle of an embedded mutagen session.
func TestEmbeddedMutagenClient_Lifecycle(t *testing.T) {
	ctx := context.Background()

	// Create temp directories for alpha and beta
	alphaDir := t.TempDir()
	betaDir := t.TempDir()

	// Create the embedded mutagen client
	client, err := NewEmbeddedMutagenClient()
	if err != nil {
		t.Skipf("embedded mutagen not available: %v", err)
	}

	// Create a sync session between two local directories
	sessionID, err := client.CreateSession(ctx, alphaDir, betaDir, SyncDirectionBidirectional)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sessionID == "" {
		t.Fatal("expected non-empty session ID")
	}

	// Give mutagen a moment to initialize
	time.Sleep(100 * time.Millisecond)

	// Check status
	status, err := client.SessionStatus(ctx, sessionID)
	if err != nil {
		t.Fatalf("SessionStatus failed: %v", err)
	}
	if status == "" {
		t.Fatal("expected non-empty status")
	}

	// Terminate the session
	if err := client.TerminateSession(ctx, sessionID); err != nil {
		t.Fatalf("TerminateSession failed: %v", err)
	}

	// Verify session is gone
	_, err = client.SessionStatus(ctx, sessionID)
	if err == nil {
		t.Fatal("expected error after terminating session")
	}
}

// TestEmbeddedMutagenClient_FileSync tests that files actually sync between directories.
func TestEmbeddedMutagenClient_FileSync(t *testing.T) {
	ctx := context.Background()

	alphaDir := t.TempDir()
	betaDir := t.TempDir()

	client, err := NewEmbeddedMutagenClient()
	if err != nil {
		t.Skipf("embedded mutagen not available: %v", err)
	}

	// Create a sync session
	sessionID, err := client.CreateSession(ctx, alphaDir, betaDir, SyncDirectionBidirectional)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer client.TerminateSession(ctx, sessionID)

	// Write a file in alpha
	testFile := filepath.Join(alphaDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello from alpha"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Wait for sync (mutagen may need a moment)
	time.Sleep(500 * time.Millisecond)

	// Verify file appears in beta
	betaFile := filepath.Join(betaDir, "test.txt")
	_, err = os.Stat(betaFile)
	if err != nil {
		t.Fatalf("file did not sync to beta: %v", err)
	}

	content, err := os.ReadFile(betaFile)
	if err != nil {
		t.Fatalf("failed to read synced file: %v", err)
	}
	if string(content) != "hello from alpha" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

// TestService_WithEmbeddedMutagen tests the service layer with real embedded mutagen.
func TestService_WithEmbeddedMutagen(t *testing.T) {
	ctx := context.Background()

	localDir := t.TempDir()
	betaDir := t.TempDir()

	client, err := NewEmbeddedMutagenClient()
	if err != nil {
		t.Skipf("embedded mutagen not available: %v", err)
	}

	repo := domainsync.NewMemoryRepository()
	lookup := newIntegrationMockWSLookup()
	lookup.workspaces["ws-1"].RootPath = betaDir

	svc := NewService(client, repo, lookup)

	// Start sync
	dto, err := svc.StartSync(ctx, "ws-1", localDir, "bidirectional")
	if err != nil {
		t.Fatalf("StartSync failed: %v", err)
	}
	if dto.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if dto.Status != "active" {
		t.Fatalf("expected status=active, got %s", dto.Status)
	}

	// Check status
	status, err := svc.SyncStatus(ctx, dto.ID, "")
	if err != nil {
		t.Fatalf("SyncStatus failed: %v", err)
	}
	if status.ID != dto.ID {
		t.Fatalf("expected status.ID=%s, got %s", dto.ID, status.ID)
	}

	// Stop sync
	if err := svc.StopSync(ctx, dto.ID, ""); err != nil {
		t.Fatalf("StopSync failed: %v", err)
	}

	// Verify stopped
	status, err = svc.SyncStatus(ctx, dto.ID, "")
	if err != nil {
		t.Fatalf("SyncStatus after stop failed: %v", err)
	}
	if status.Status != "stopped" {
		t.Fatalf("expected status=stopped, got %s", status.Status)
	}
}

// TestService_SyncAlreadyActive tests that starting sync twice for the same workspace fails.
func TestService_SyncAlreadyActive(t *testing.T) {
	ctx := context.Background()

	localDir := t.TempDir()
	betaDir := t.TempDir()

	client, err := NewEmbeddedMutagenClient()
	if err != nil {
		t.Skipf("embedded mutagen not available: %v", err)
	}

	repo := domainsync.NewMemoryRepository()
	lookup := newIntegrationMockWSLookup()
	lookup.workspaces["ws-1"].RootPath = betaDir

	svc := NewService(client, repo, lookup)

	// First sync should succeed
	_, err = svc.StartSync(ctx, "ws-1", localDir, "bidirectional")
	if err != nil {
		t.Fatalf("first StartSync failed: %v", err)
	}

	// Second sync for same workspace should fail
	_, err = svc.StartSync(ctx, "ws-1", localDir, "bidirectional")
	if err == nil {
		t.Fatal("expected error when starting sync for workspace that already has active sync")
	}
}

// TestService_PathNotAccessible tests that starting sync with non-existent path fails.
func TestService_PathNotAccessible(t *testing.T) {
	ctx := context.Background()

	client, err := NewEmbeddedMutagenClient()
	if err != nil {
		t.Skipf("embedded mutagen not available: %v", err)
	}

	repo := domainsync.NewMemoryRepository()
	lookup := newIntegrationMockWSLookup()
	svc := NewService(client, repo, lookup)

	// Sync with non-existent path should fail
	_, err = svc.StartSync(ctx, "ws-1", "/nonexistent/path/12345", "bidirectional")
	if err == nil {
		t.Fatal("expected error when starting sync with non-existent path")
	}
}
