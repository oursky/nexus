package sync

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	domainsync "github.com/oursky/nexus/packages/nexus/internal/domain/sync"
)

func TestSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sync", "sessions.json")

	store := NewSessionStore(path)

	// Add some sessions
	sess1 := &domainsync.SyncSession{
		ID:          "sess-1",
		WorkspaceID: "ws-1",
		LocalPath:   "/tmp/local1",
		Status:      domainsync.SyncStatusActive,
		Direction:   domainsync.SyncDirectionUp,
		Alpha:       "/tmp/local1",
		Beta:        "user@host:/remote1",
		MutagenID:   "mutagen-1",
		StartedAt:   time.Now().UTC().Truncate(time.Second),
		Stats: domainsync.SyncStats{
			TotalSyncs: 5,
			BytesSent:  1024,
		},
	}
	sess2 := &domainsync.SyncSession{
		ID:          "sess-2",
		WorkspaceID: "ws-2",
		LocalPath:   "/tmp/local2",
		Status:      domainsync.SyncStatusStopped,
		Direction:   domainsync.SyncDirectionBidirectional,
		Alpha:       "/tmp/local2",
		Beta:        "user@host:/remote2",
		MutagenID:   "mutagen-2",
		StartedAt:   time.Now().UTC().Truncate(time.Second),
		StoppedAt:   ptr(time.Now().UTC().Truncate(time.Second)),
		LastSyncAt:  ptr(time.Now().UTC().Truncate(time.Second)),
		Stats: domainsync.SyncStats{
			TotalSyncs:        10,
			BytesReceived:     2048,
			FilesSent:         3,
			FilesReceived:     7,
			ConflictsResolved: 1,
		},
	}

	if err := store.Add(sess1); err != nil {
		t.Fatalf("Add sess1: %v", err)
	}
	if err := store.Add(sess2); err != nil {
		t.Fatalf("Add sess2: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file not created: %v", err)
	}

	// Load into a new store
	store2 := NewSessionStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify sessions loaded correctly
	loaded1, ok := store2.Get("sess-1")
	if !ok {
		t.Fatal("sess-1 not found after load")
	}
	if loaded1.ID != sess1.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded1.ID, sess1.ID)
	}
	if loaded1.WorkspaceID != sess1.WorkspaceID {
		t.Errorf("WorkspaceID mismatch: got %q, want %q", loaded1.WorkspaceID, sess1.WorkspaceID)
	}
	if loaded1.Status != sess1.Status {
		t.Errorf("Status mismatch: got %q, want %q", loaded1.Status, sess1.Status)
	}
	if loaded1.Direction != sess1.Direction {
		t.Errorf("Direction mismatch: got %q, want %q", loaded1.Direction, sess1.Direction)
	}
	if loaded1.Alpha != sess1.Alpha {
		t.Errorf("Alpha mismatch: got %q, want %q", loaded1.Alpha, sess1.Alpha)
	}
	if loaded1.Beta != sess1.Beta {
		t.Errorf("Beta mismatch: got %q, want %q", loaded1.Beta, sess1.Beta)
	}
	if loaded1.MutagenID != sess1.MutagenID {
		t.Errorf("MutagenID mismatch: got %q, want %q", loaded1.MutagenID, sess1.MutagenID)
	}
	if !loaded1.StartedAt.Equal(sess1.StartedAt) {
		t.Errorf("StartedAt mismatch: got %v, want %v", loaded1.StartedAt, sess1.StartedAt)
	}
	if loaded1.StoppedAt != nil {
		t.Errorf("StoppedAt should be nil, got %v", loaded1.StoppedAt)
	}
	if loaded1.LastSyncAt != nil {
		t.Errorf("LastSyncAt should be nil, got %v", loaded1.LastSyncAt)
	}
	if loaded1.Stats.TotalSyncs != sess1.Stats.TotalSyncs {
		t.Errorf("Stats.TotalSyncs mismatch: got %d, want %d", loaded1.Stats.TotalSyncs, sess1.Stats.TotalSyncs)
	}
	if loaded1.Stats.BytesSent != sess1.Stats.BytesSent {
		t.Errorf("Stats.BytesSent mismatch: got %d, want %d", loaded1.Stats.BytesSent, sess1.Stats.BytesSent)
	}

	loaded2, ok := store2.Get("sess-2")
	if !ok {
		t.Fatal("sess-2 not found after load")
	}
	if loaded2.Status != domainsync.SyncStatusStopped {
		t.Errorf("Status mismatch: got %q, want %q", loaded2.Status, domainsync.SyncStatusStopped)
	}
	if loaded2.StoppedAt == nil {
		t.Fatal("StoppedAt should not be nil")
	}
	if !loaded2.StoppedAt.Equal(*sess2.StoppedAt) {
		t.Errorf("StoppedAt mismatch: got %v, want %v", *loaded2.StoppedAt, *sess2.StoppedAt)
	}
	if loaded2.LastSyncAt == nil {
		t.Fatal("LastSyncAt should not be nil")
	}
	if !loaded2.LastSyncAt.Equal(*sess2.LastSyncAt) {
		t.Errorf("LastSyncAt mismatch: got %v, want %v", *loaded2.LastSyncAt, *sess2.LastSyncAt)
	}
	if loaded2.Stats.FilesSent != sess2.Stats.FilesSent {
		t.Errorf("Stats.FilesSent mismatch: got %d, want %d", loaded2.Stats.FilesSent, sess2.Stats.FilesSent)
	}
	if loaded2.Stats.FilesReceived != sess2.Stats.FilesReceived {
		t.Errorf("Stats.FilesReceived mismatch: got %d, want %d", loaded2.Stats.FilesReceived, sess2.Stats.FilesReceived)
	}
	if loaded2.Stats.ConflictsResolved != sess2.Stats.ConflictsResolved {
		t.Errorf("Stats.ConflictsResolved mismatch: got %d, want %d", loaded2.Stats.ConflictsResolved, sess2.Stats.ConflictsResolved)
	}
}

func TestAddRemove(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sync", "sessions.json")

	store := NewSessionStore(path)

	sess := &domainsync.SyncSession{
		ID:          "sess-add",
		WorkspaceID: "ws-add",
		LocalPath:   "/tmp/add",
		Status:      domainsync.SyncStatusActive,
		Direction:   domainsync.SyncDirectionDown,
		StartedAt:   time.Now().UTC(),
	}

	if err := store.Add(sess); err != nil {
		t.Fatalf("Add: %v", err)
	}

	loaded, ok := store.Get("sess-add")
	if !ok {
		t.Fatal("session not found after add")
	}
	if loaded.ID != sess.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded.ID, sess.ID)
	}

	if err := store.Remove("sess-add"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, ok = store.Get("sess-add")
	if ok {
		t.Fatal("session should not exist after remove")
	}

	// Verify file was updated
	store2 := NewSessionStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load after remove: %v", err)
	}
	_, ok = store2.Get("sess-add")
	if ok {
		t.Fatal("session should not exist in file after remove")
	}
}

func TestUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sync", "sessions.json")

	store := NewSessionStore(path)

	sess := &domainsync.SyncSession{
		ID:          "sess-update",
		WorkspaceID: "ws-update",
		LocalPath:   "/tmp/update",
		Status:      domainsync.SyncStatusActive,
		Direction:   domainsync.SyncDirectionUp,
		StartedAt:   time.Now().UTC(),
		Stats: domainsync.SyncStats{
			TotalSyncs: 1,
		},
	}

	if err := store.Add(sess); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Update the session
	sess.Status = domainsync.SyncStatusPaused
	sess.Stats.TotalSyncs = 5
	sess.Stats.BytesSent = 4096
	now := time.Now().UTC().Truncate(time.Second)
	sess.LastSyncAt = &now

	if err := store.Update(sess); err != nil {
		t.Fatalf("Update: %v", err)
	}

	loaded, ok := store.Get("sess-update")
	if !ok {
		t.Fatal("session not found after update")
	}
	if loaded.Status != domainsync.SyncStatusPaused {
		t.Errorf("Status mismatch: got %q, want %q", loaded.Status, domainsync.SyncStatusPaused)
	}
	if loaded.Stats.TotalSyncs != 5 {
		t.Errorf("TotalSyncs mismatch: got %d, want %d", loaded.Stats.TotalSyncs, 5)
	}
	if loaded.Stats.BytesSent != 4096 {
		t.Errorf("BytesSent mismatch: got %d, want %d", loaded.Stats.BytesSent, 4096)
	}
	if loaded.LastSyncAt == nil {
		t.Fatal("LastSyncAt should not be nil")
	}
	if !loaded.LastSyncAt.Equal(now) {
		t.Errorf("LastSyncAt mismatch: got %v, want %v", *loaded.LastSyncAt, now)
	}

	// Verify file was updated
	store2 := NewSessionStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load after update: %v", err)
	}
	loaded2, ok := store2.Get("sess-update")
	if !ok {
		t.Fatal("session not found in file after update")
	}
	if loaded2.Status != domainsync.SyncStatusPaused {
		t.Errorf("File Status mismatch: got %q, want %q", loaded2.Status, domainsync.SyncStatusPaused)
	}
	if loaded2.Stats.TotalSyncs != 5 {
		t.Errorf("File TotalSyncs mismatch: got %d, want %d", loaded2.Stats.TotalSyncs, 5)
	}
}

func TestLoadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nonexistent", "sessions.json")

	store := NewSessionStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("Load non-existent file should not error: %v", err)
	}

	if len(store.List()) != 0 {
		t.Fatalf("expected empty store, got %d sessions", len(store.List()))
	}
}

func TestList(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sync", "sessions.json")

	store := NewSessionStore(path)

	for i := 0; i < 3; i++ {
		sess := &domainsync.SyncSession{
			ID:        string(rune('a' + i)),
			Status:    domainsync.SyncStatusActive,
			StartedAt: time.Now().UTC(),
		}
		if err := store.Add(sess); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	sessions := store.List()
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
}

func ptr(t time.Time) *time.Time {
	return &t
}
