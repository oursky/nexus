package clientstate_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/clientstate"
)

func openTestDB(t *testing.T) *clientstate.DB {
	t.Helper()
	db, err := clientstate.OpenAt(filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ── Open ──────────────────────────────────────────────────────────────────────

func TestOpen_CreatesTablesAndReturnsDB(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestOpen_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.db")
	for i := 0; i < 3; i++ {
		db, err := clientstate.OpenAt(path)
		if err != nil {
			t.Fatalf("OpenAt (attempt %d): %v", i+1, err)
		}
		_ = db.Close()
	}
}

// ── Profiles ──────────────────────────────────────────────────────────────────

func TestUpsertProfile_GetProfile_RoundTrip(t *testing.T) {
	db := openTestDB(t)

	p := clientstate.Profile{
		Name:            "default",
		Host:            "newman@linuxbox",
		Port:            7777,
		SSHPort:         2222,
		SSHIdentityFile: "/home/newman/.ssh/id_ed25519",
		LocalPort:       40000,
		IsDefault:       true,
	}
	if err := db.UpsertProfile(p); err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}

	got, err := db.GetProfile("default")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Host != p.Host || got.Port != p.Port || got.SSHPort != p.SSHPort ||
		got.SSHIdentityFile != p.SSHIdentityFile || got.LocalPort != p.LocalPort ||
		got.IsDefault != p.IsDefault {
		t.Errorf("round-trip mismatch:\ngot  %+v\nwant %+v", got, p)
	}
}

func TestUpsertProfile_Update(t *testing.T) {
	db := openTestDB(t)

	p1 := clientstate.Profile{Name: "default", Host: "old-host", Port: 7777, IsDefault: true}
	if err := db.UpsertProfile(p1); err != nil {
		t.Fatalf("UpsertProfile (initial): %v", err)
	}

	p2 := clientstate.Profile{Name: "default", Host: "new-host", Port: 7778, IsDefault: true}
	if err := db.UpsertProfile(p2); err != nil {
		t.Fatalf("UpsertProfile (update): %v", err)
	}

	got, err := db.GetProfile("default")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Host != "new-host" || got.Port != 7778 {
		t.Errorf("expected updated values, got %+v", got)
	}
}

func TestGetDefaultProfile_ReturnsIsDefaultRow(t *testing.T) {
	db := openTestDB(t)

	_ = db.UpsertProfile(clientstate.Profile{Name: "other", Host: "host-a", Port: 7777, IsDefault: false})
	_ = db.UpsertProfile(clientstate.Profile{Name: "default", Host: "host-b", Port: 7777, IsDefault: true})

	got, err := db.GetDefaultProfile()
	if err != nil {
		t.Fatalf("GetDefaultProfile: %v", err)
	}
	if got == nil {
		t.Fatal("expected profile, got nil")
	}
	if got.Name != "default" {
		t.Errorf("expected 'default', got %q", got.Name)
	}
}

func TestGetDefaultProfile_FallsBackToFirstRow(t *testing.T) {
	db := openTestDB(t)

	_ = db.UpsertProfile(clientstate.Profile{Name: "first", Host: "h1", Port: 7777, IsDefault: false})

	got, err := db.GetDefaultProfile()
	if err != nil {
		t.Fatalf("GetDefaultProfile: %v", err)
	}
	if got == nil {
		t.Fatal("expected profile, got nil")
	}
	if got.Name != "first" {
		t.Errorf("expected 'first', got %q", got.Name)
	}
}

func TestGetDefaultProfile_EmptyTable(t *testing.T) {
	db := openTestDB(t)

	got, err := db.GetDefaultProfile()
	if err != nil {
		t.Fatalf("GetDefaultProfile: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestDeleteProfile(t *testing.T) {
	db := openTestDB(t)

	_ = db.UpsertProfile(clientstate.Profile{Name: "default", Host: "h", Port: 7777, IsDefault: true})
	if err := db.DeleteProfile("default"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}

	got, err := db.GetDefaultProfile()
	if err != nil {
		t.Fatalf("GetDefaultProfile after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestDeleteProfile_NotFound_NoError(t *testing.T) {
	db := openTestDB(t)
	if err := db.DeleteProfile("nonexistent"); err != nil {
		t.Errorf("expected no error deleting nonexistent profile, got %v", err)
	}
}

func TestListProfiles(t *testing.T) {
	db := openTestDB(t)

	names := []string{"a", "b", "c"}
	for _, n := range names {
		_ = db.UpsertProfile(clientstate.Profile{Name: n, Host: "h", Port: 7777})
	}

	profiles, err := db.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != len(names) {
		t.Fatalf("expected %d profiles, got %d", len(names), len(profiles))
	}
	for i, p := range profiles {
		if p.Name != names[i] {
			t.Errorf("index %d: expected %q, got %q", i, names[i], p.Name)
		}
	}
}

// ── TUI sessions ─────────────────────────────────────────────────────────────

func TestTUISession_UpsertAndGet(t *testing.T) {
	db := openTestDB(t)

	s := clientstate.TUISession{
		WorkspaceID:   "ws-001",
		WorkspaceName: "myws",
		LastAttached:  time.Now().UTC().Truncate(time.Second),
		TabOrder:      0,
	}
	if err := db.UpsertTUISession(s); err != nil {
		t.Fatalf("UpsertTUISession: %v", err)
	}

	sessions, err := db.GetTUISessions()
	if err != nil {
		t.Fatalf("GetTUISessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	got := sessions[0]
	if got.WorkspaceID != s.WorkspaceID || got.WorkspaceName != s.WorkspaceName ||
		got.TabOrder != s.TabOrder {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, s)
	}
}

func TestTUISession_DeleteSession(t *testing.T) {
	db := openTestDB(t)

	_ = db.UpsertTUISession(clientstate.TUISession{WorkspaceID: "ws-001", TabOrder: 0})
	_ = db.UpsertTUISession(clientstate.TUISession{WorkspaceID: "ws-002", TabOrder: 1})

	if err := db.DeleteTUISession("ws-001"); err != nil {
		t.Fatalf("DeleteTUISession: %v", err)
	}

	sessions, _ := db.GetTUISessions()
	if len(sessions) != 1 || sessions[0].WorkspaceID != "ws-002" {
		t.Errorf("expected only ws-002 after delete, got %+v", sessions)
	}
}

func TestTUISession_ReplaceAll(t *testing.T) {
	db := openTestDB(t)

	initial := []clientstate.TUISession{
		{WorkspaceID: "ws-a", TabOrder: 0},
		{WorkspaceID: "ws-b", TabOrder: 1},
		{WorkspaceID: "ws-c", TabOrder: 2},
	}
	if err := db.ReplaceAllTUISessions(initial); err != nil {
		t.Fatalf("ReplaceAllTUISessions (initial): %v", err)
	}

	updated := []clientstate.TUISession{
		{WorkspaceID: "ws-x", TabOrder: 0},
		{WorkspaceID: "ws-y", TabOrder: 1},
	}
	if err := db.ReplaceAllTUISessions(updated); err != nil {
		t.Fatalf("ReplaceAllTUISessions (replace): %v", err)
	}

	sessions, err := db.GetTUISessions()
	if err != nil {
		t.Fatalf("GetTUISessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions after replace, got %d", len(sessions))
	}
	if sessions[0].WorkspaceID != "ws-x" || sessions[1].WorkspaceID != "ws-y" {
		t.Errorf("unexpected sessions after replace: %+v", sessions)
	}
}

func TestTUIPrefs_SetAndGet(t *testing.T) {
	db := openTestDB(t)

	if err := db.SetTUIPref("active_workspace", "ws-123"); err != nil {
		t.Fatalf("SetTUIPref: %v", err)
	}

	got, err := db.GetTUIPref("active_workspace")
	if err != nil {
		t.Fatalf("GetTUIPref: %v", err)
	}
	if got != "ws-123" {
		t.Errorf("expected %q, got %q", "ws-123", got)
	}
}

func TestTUIPrefs_MissingKey_ReturnsEmpty(t *testing.T) {
	db := openTestDB(t)

	got, err := db.GetTUIPref("no_such_key")
	if err != nil {
		t.Fatalf("GetTUIPref: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// ── Workspace cache ───────────────────────────────────────────────────────────

func TestWorkspaceCache_ReplaceAll(t *testing.T) {
	db := openTestDB(t)

	initial := []clientstate.CachedWorkspace{
		{WorkspaceID: "ws-1", ProfileName: "default", Name: "alpha", State: "running"},
		{WorkspaceID: "ws-2", ProfileName: "default", Name: "beta", State: "stopped"},
	}
	if err := db.CacheWorkspaces("default", initial); err != nil {
		t.Fatalf("CacheWorkspaces (initial): %v", err)
	}

	// Replace with a single entry — ws-1 must disappear.
	updated := []clientstate.CachedWorkspace{
		{WorkspaceID: "ws-3", ProfileName: "default", Name: "gamma", State: "running"},
	}
	if err := db.CacheWorkspaces("default", updated); err != nil {
		t.Fatalf("CacheWorkspaces (replace): %v", err)
	}

	cached, err := db.GetCachedWorkspaces("default")
	if err != nil {
		t.Fatalf("GetCachedWorkspaces: %v", err)
	}
	if len(cached) != 1 || cached[0].WorkspaceID != "ws-3" {
		t.Errorf("expected single ws-3 entry, got %+v", cached)
	}
}

func TestWorkspaceCache_ProfileIsolation(t *testing.T) {
	db := openTestDB(t)

	_ = db.CacheWorkspaces("profile-a", []clientstate.CachedWorkspace{
		{WorkspaceID: "ws-a1", ProfileName: "profile-a", Name: "a1", State: "running"},
	})
	_ = db.CacheWorkspaces("profile-b", []clientstate.CachedWorkspace{
		{WorkspaceID: "ws-b1", ProfileName: "profile-b", Name: "b1", State: "running"},
	})

	// Replace profile-a — profile-b must be untouched.
	_ = db.CacheWorkspaces("profile-a", nil)

	aRows, _ := db.GetCachedWorkspaces("profile-a")
	bRows, _ := db.GetCachedWorkspaces("profile-b")

	if len(aRows) != 0 {
		t.Errorf("expected 0 rows for profile-a, got %d", len(aRows))
	}
	if len(bRows) != 1 {
		t.Errorf("expected 1 row for profile-b, got %d", len(bRows))
	}
}

func TestWorkspaceCache_Invalidate(t *testing.T) {
	db := openTestDB(t)

	_ = db.CacheWorkspaces("default", []clientstate.CachedWorkspace{
		{WorkspaceID: "ws-1", ProfileName: "default", Name: "one", State: "running"},
	})

	if err := db.InvalidateCache("default"); err != nil {
		t.Fatalf("InvalidateCache: %v", err)
	}

	cached, _ := db.GetCachedWorkspaces("default")
	if len(cached) != 0 {
		t.Errorf("expected empty cache after invalidate, got %d rows", len(cached))
	}
}
