package sync

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	domainsync "github.com/oursky/nexus/packages/nexus/internal/domain/sync"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// testLocalPath creates a temp directory for use as a local sync path.
func testLocalPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nexus-sync-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// mockWSLookup is a test double for WorkspaceLookup.
type mockWSLookup struct {
	workspaces map[string]*domainws.Workspace
}

func newMockWSLookup() *mockWSLookup {
	return &mockWSLookup{
		workspaces: map[string]*domainws.Workspace{
			"ws-1": {
				ID:       "ws-1",
				RootPath: "/workspace",
				GuestIP:  "127.0.0.1:2222",
				State:    domainws.StateRunning,
			},
			"ws-2": {
				ID:       "ws-2",
				RootPath: "/workspace",
				GuestIP:  "127.0.0.1:2223",
				State:    domainws.StateRunning,
			},
		},
	}
}

func (m *mockWSLookup) Get(_ context.Context, id string) (*domainws.Workspace, error) {
	ws, ok := m.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found", id)
	}
	return ws, nil
}

func TestNewService(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestStartSync_NoMutagen(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	dto, err := svc.StartSync(ctx, "ws-1", testLocalPath(t), "bidirectional")
	if err != nil {
		t.Fatalf("StartSync failed: %v", err)
	}
	if dto.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if dto.WorkspaceID != "ws-1" {
		t.Fatalf("expected workspaceID=ws-1, got %s", dto.WorkspaceID)
	}
	if dto.LocalPath == "" {
		t.Fatalf("expected localPath=/tmp/local, got %s", dto.LocalPath)
	}
	if dto.Status != "active" {
		t.Fatalf("expected status=active, got %s", dto.Status)
	}
	if dto.Direction != "bidirectional" {
		t.Fatalf("expected direction=bidirectional, got %s", dto.Direction)
	}
	// Beta should be the SSH URL, not the workspaceID
	if dto.Beta != "user@127.0.0.1:2222:/workspace" {
		t.Fatalf("expected beta=user@127.0.0.1:2222:/workspace, got %s", dto.Beta)
	}
}

func TestStartSync_InvalidDirection(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	_, err := svc.StartSync(ctx, "ws-1", testLocalPath(t), "invalid")
	if err == nil {
		t.Fatal("expected error for invalid direction")
	}
}

func TestStartSync_WorkspaceNotFound(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	_, err := svc.StartSync(ctx, "ws-nonexistent", testLocalPath(t), "bidirectional")
	if err == nil {
		t.Fatal("expected error for nonexistent workspace")
	}
}

func TestStartSync_WorkspaceNotRunning(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	lookup := newMockWSLookup()
	lookup.workspaces["ws-stopped"] = &domainws.Workspace{
		ID:       "ws-stopped",
		RootPath: "", // empty RootPath with empty GuestIP = not running
		GuestIP:  "", // not running
	}
	svc := NewService(nil, repo, lookup)
	ctx := context.Background()

	_, err := svc.StartSync(ctx, "ws-stopped", testLocalPath(t), "bidirectional")
	if err == nil {
		t.Fatal("expected error for non-running workspace")
	}
}

func TestNewService_NilLookup(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when WorkspaceLookup is nil")
		}
	}()
	repo := domainsync.NewMemoryRepository()
	NewService(nil, repo, nil)
}

func TestStopSync_BySessionID(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	dto, err := svc.StartSync(ctx, "ws-1", testLocalPath(t), "bidirectional")
	if err != nil {
		t.Fatalf("StartSync failed: %v", err)
	}

	if err := svc.StopSync(ctx, dto.ID, ""); err != nil {
		t.Fatalf("StopSync failed: %v", err)
	}

	// Verify session is stopped
	status, err := svc.SyncStatus(ctx, dto.ID, "")
	if err != nil {
		t.Fatalf("SyncStatus failed: %v", err)
	}
	if status.Status != "stopped" {
		t.Fatalf("expected status=stopped, got %s", status.Status)
	}
}

func TestStopSync_ByWorkspaceID(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	_, err := svc.StartSync(ctx, "ws-1", testLocalPath(t), "bidirectional")
	if err != nil {
		t.Fatalf("StartSync 1 failed: %v", err)
	}
	// Create second session for ws-2 (not ws-1, since ws-1 already has active sync)
	_, err = svc.StartSync(ctx, "ws-2", testLocalPath(t), "up")
	if err != nil {
		t.Fatalf("StartSync 2 failed: %v", err)
	}

	if err := svc.StopSync(ctx, "", "ws-1"); err != nil {
		t.Fatalf("StopSync failed: %v", err)
	}

	sessions, err := svc.ListSyncs(ctx, "ws-1")
	if err != nil {
		t.Fatalf("ListSyncs failed: %v", err)
	}
	for _, s := range sessions {
		if s.Status != "stopped" {
			t.Fatalf("expected session %s to be stopped, got %s", s.ID, s.Status)
		}
	}
}

func TestStopSync_NotFound(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	err := svc.StopSync(ctx, "nonexistent", "")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestSyncStatus_NoSessionsForWorkspace(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	_, err := svc.SyncStatus(ctx, "", "ws-nope")
	if err == nil {
		t.Fatal("expected error for no sessions")
	}
}

func TestSyncStatus_SessionNotFound(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	_, err := svc.SyncStatus(ctx, "nonexistent", "")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestListSyncs_All(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	_, _ = svc.StartSync(ctx, "ws-1", testLocalPath(t), "bidirectional")
	_, _ = svc.StartSync(ctx, "ws-2", testLocalPath(t), "up")

	sessions, err := svc.ListSyncs(ctx, "")
	if err != nil {
		t.Fatalf("ListSyncs failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestListSyncs_ByWorkspace(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	_, _ = svc.StartSync(ctx, "ws-1", testLocalPath(t), "bidirectional")
	_, _ = svc.StartSync(ctx, "ws-2", testLocalPath(t), "up")
	// Note: ws-1 already has active sync, so we can't create another

	sessions, err := svc.ListSyncs(ctx, "ws-1")
	if err != nil {
		t.Fatalf("ListSyncs failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session for ws-1, got %d", len(sessions))
	}
}

func TestListSyncs_Empty(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	sessions, err := svc.ListSyncs(ctx, "")
	if err != nil {
		t.Fatalf("ListSyncs failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestDirectionToMode(t *testing.T) {
	tests := []struct {
		input    string
		expected SyncDirection
		wantErr  bool
	}{
		{"bidirectional", SyncDirectionBidirectional, false},
		{"", SyncDirectionBidirectional, false},
		{"up", SyncDirectionUp, false},
		{"down", SyncDirectionDown, false},
		{"sideways", 0, true},
	}
	for _, tt := range tests {
		mode, err := directionToMode(domainsync.SyncDirection(tt.input))
		if tt.wantErr {
			if err == nil {
				t.Errorf("directionToMode(%q): expected error", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("directionToMode(%q): unexpected error: %v", tt.input, err)
			}
			if mode != tt.expected {
				t.Errorf("directionToMode(%q): expected %d, got %d", tt.input, tt.expected, mode)
			}
		}
	}
}

func TestSyncDirection_Up(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	localPath := testLocalPath(t)
	dto, err := svc.StartSync(ctx, "ws-1", localPath, "up")
	if err != nil {
		t.Fatalf("StartSync failed: %v", err)
	}
	if dto.Direction != "up" {
		t.Fatalf("expected direction=up, got %s", dto.Direction)
	}
	// For "up": alpha=local, beta=remote
	if dto.Alpha != localPath {
		t.Fatalf("expected alpha to match test path, got %s", dto.Alpha)
	}
}

func TestSyncDirection_Down(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	localPath := testLocalPath(t)
	dto, err := svc.StartSync(ctx, "ws-1", localPath, "down")
	if err != nil {
		t.Fatalf("StartSync failed: %v", err)
	}
	if dto.Direction != "down" {
		t.Fatalf("expected direction=down, got %s", dto.Direction)
	}
	// For "down": alpha=remote, beta=local (swapped)
	if dto.Beta != localPath {
		t.Fatalf("expected beta to match test path, got %s", dto.Beta)
	}
}

// mockSyncMutagen implements MutagenClientInterface for pause/resume tests.
type mockSyncMutagen struct {
	mu          sync.Mutex
	pauseCalls  []string
	resumeCalls []string
	pauseErr    error
	resumeErr   error
}

func newMockSyncMutagen() *mockSyncMutagen {
	return &mockSyncMutagen{}
}

func (m *mockSyncMutagen) CreateSession(_ context.Context, _, _ string, _ SyncDirection) (string, error) {
	return "mutagen-" + uuid.New().String()[:8], nil
}

func (m *mockSyncMutagen) TerminateSession(_ context.Context, _ string) error {
	return nil
}

func (m *mockSyncMutagen) SessionStatus(_ context.Context, _ string) (string, error) {
	return "idle", nil
}

func (m *mockSyncMutagen) PauseSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pauseCalls = append(m.pauseCalls, sessionID)
	return m.pauseErr
}

func (m *mockSyncMutagen) ResumeSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resumeCalls = append(m.resumeCalls, sessionID)
	return m.resumeErr
}

func (m *mockSyncMutagen) PauseCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pauseCalls)
}
func (m *mockSyncMutagen) ResumeCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.resumeCalls)
}

// createActiveSessionWithMutagen creates an active session with a MutagenID for pause/resume tests.
func createActiveSessionWithMutagen(t *testing.T, repo domainsync.Repository, wsID string) *domainsync.SyncSession {
	t.Helper()
	sess := &domainsync.SyncSession{
		ID:          uuid.New().String(),
		WorkspaceID: wsID,
		LocalPath:   testLocalPath(t),
		Status:      domainsync.SyncStatusActive,
		Direction:   domainsync.SyncDirectionBidirectional,
		Alpha:       "/tmp/alpha",
		Beta:        "/tmp/beta",
		MutagenID:   "mutagen-test-123",
		StartedAt:   time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess
}

// --- P2: Pause/Resume Guards ---

func TestPauseSync_ActiveSession(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")
	err := svc.PauseSync(ctx, sess.ID)
	if err != nil {
		t.Fatalf("PauseSync: %v", err)
	}
	if mc.PauseCallCount() != 1 {
		t.Fatalf("expected 1 PauseSession call, got %d", mc.PauseCallCount())
	}
	// Verify status persisted
	got, _ := repo.Get(ctx, sess.ID)
	if got.Status != domainsync.SyncStatusPaused {
		t.Fatalf("expected status=paused, got %s", got.Status)
	}
}

func TestPauseSync_AlreadyPaused(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")
	_ = svc.PauseSync(ctx, sess.ID)

	err := svc.PauseSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error for already paused")
	}
	if !contains(err.Error(), "already paused") {
		t.Fatalf("expected 'already paused' error, got: %v", err)
	}
}

func TestPauseSync_StoppedSession(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")
	_ = svc.StopSync(ctx, sess.ID, "")

	err := svc.PauseSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error for stopped session")
	}
	if !contains(err.Error(), "cannot pause") {
		t.Fatalf("expected 'cannot pause' error, got: %v", err)
	}
}

func TestResumeSync_PausedSession(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")
	_ = svc.PauseSync(ctx, sess.ID)

	err := svc.ResumeSync(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ResumeSync: %v", err)
	}
	if mc.ResumeCallCount() != 1 {
		t.Fatalf("expected 1 ResumeSession call, got %d", mc.ResumeCallCount())
	}
	got, _ := repo.Get(ctx, sess.ID)
	if got.Status != domainsync.SyncStatusActive {
		t.Fatalf("expected status=active, got %s", got.Status)
	}
}

func TestResumeSync_ActiveSession(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")

	err := svc.ResumeSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error for non-paused session")
	}
	if !contains(err.Error(), "not paused") {
		t.Fatalf("expected 'not paused' error, got: %v", err)
	}
}

func TestResumeSync_StoppedSession(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")
	_ = svc.StopSync(ctx, sess.ID, "")

	err := svc.ResumeSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error for stopped session")
	}
	if !contains(err.Error(), "not paused") {
		t.Fatalf("expected 'not paused' error, got: %v", err)
	}
}

// --- P4: Pause/Resume Forwarded to Mutagen ---

func TestPauseSync_MutagenError(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	mc.pauseErr = fmt.Errorf("mutagen internal error")
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")
	err := svc.PauseSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error from mutagen")
	}
	// Status should NOT have been mutated
	got, _ := repo.Get(ctx, sess.ID)
	if got.Status != domainsync.SyncStatusActive {
		t.Fatalf("expected status to remain active, got %s", got.Status)
	}
}

func TestResumeSync_MutagenError(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	mc.resumeErr = fmt.Errorf("mutagen internal error")
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")
	_ = svc.PauseSync(ctx, sess.ID)

	err := svc.ResumeSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error from mutagen")
	}
	got, _ := repo.Get(ctx, sess.ID)
	if got.Status != domainsync.SyncStatusPaused {
		t.Fatalf("expected status to remain paused, got %s", got.Status)
	}
}

// --- P7: Nil Mutagen Graceful Degradation ---

func TestPauseSync_NilMutagen(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	sess := createActiveSessionWithMutagen(t, repo, "ws-1")
	err := svc.PauseSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error for nil mutagen")
	}
	if !contains(err.Error(), "mutagen not available") {
		t.Fatalf("expected 'mutagen not available' error, got: %v", err)
	}
}

func TestResumeSync_NilMutagen(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	svc := NewService(nil, repo, newMockWSLookup())
	ctx := context.Background()

	// Create session directly as paused (since we can't use PauseSync without mutagen)
	sess := &domainsync.SyncSession{
		ID:          uuid.New().String(),
		WorkspaceID: "ws-1",
		LocalPath:   "/tmp/test",
		Status:      domainsync.SyncStatusPaused,
		Direction:   domainsync.SyncDirectionBidirectional,
		MutagenID:   "mutagen-test-456",
		StartedAt:   time.Now().UTC(),
	}
	_ = repo.Create(ctx, sess)

	err := svc.ResumeSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error for nil mutagen")
	}
	if !contains(err.Error(), "mutagen not available") {
		t.Fatalf("expected 'mutagen not available' error, got: %v", err)
	}
}

// --- P4: No MutagenID ---

func TestPauseSync_NoMutagenID(t *testing.T) {
	repo := domainsync.NewMemoryRepository()
	mc := newMockSyncMutagen()
	svc := NewService(mc, repo, newMockWSLookup())
	ctx := context.Background()

	// Create session without MutagenID
	sess := &domainsync.SyncSession{
		ID:          uuid.New().String(),
		WorkspaceID: "ws-1",
		LocalPath:   "/tmp/test",
		Status:      domainsync.SyncStatusActive,
		Direction:   domainsync.SyncDirectionBidirectional,
		MutagenID:   "",
		StartedAt:   time.Now().UTC(),
	}
	_ = repo.Create(ctx, sess)

	err := svc.PauseSync(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error for no mutagen ID")
	}
	if !contains(err.Error(), "no mutagen session") {
		t.Fatalf("expected 'no mutagen session' error, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
