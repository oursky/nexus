package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	domainsync "github.com/oursky/nexus/packages/nexus/internal/domain/sync"
)

// SessionStore persists sync sessions to a JSON file on disk.
type SessionStore struct {
	mu       sync.RWMutex
	path     string
	sessions map[string]*domainsync.SyncSession
}

// persistedSession is the JSON-serializable representation of a sync session.
type persistedSession struct {
	ID          string               `json:"id"`
	WorkspaceID string               `json:"workspace_id"`
	LocalPath   string               `json:"local_path"`
	Status      string               `json:"status"`
	Direction   string               `json:"direction"`
	Alpha       string               `json:"alpha"`
	Beta        string               `json:"beta"`
	MutagenID   string               `json:"mutagen_id"`
	StartedAt   time.Time            `json:"started_at"`
	StoppedAt   *time.Time           `json:"stopped_at,omitempty"`
	LastSyncAt  *time.Time           `json:"last_sync_at,omitempty"`
	Stats       domainsync.SyncStats `json:"stats"`
}

// NewSessionStore creates a new SessionStore that persists to the given path.
func NewSessionStore(path string) *SessionStore {
	return &SessionStore{
		path:     path,
		sessions: make(map[string]*domainsync.SyncSession),
	}
}

// Load reads sessions from disk. If the file does not exist, the store is empty.
func (st *SessionStore) Load() error {
	st.mu.Lock()
	defer st.mu.Unlock()

	data, err := os.ReadFile(st.path)
	if err != nil {
		if os.IsNotExist(err) {
			st.sessions = make(map[string]*domainsync.SyncSession)
			return nil
		}
		return fmt.Errorf("sync: read session store: %w", err)
	}

	var persisted []persistedSession
	if err := json.Unmarshal(data, &persisted); err != nil {
		return fmt.Errorf("sync: unmarshal session store: %w", err)
	}

	st.sessions = make(map[string]*domainsync.SyncSession, len(persisted))
	for _, p := range persisted {
		sess := &domainsync.SyncSession{
			ID:          p.ID,
			WorkspaceID: p.WorkspaceID,
			LocalPath:   p.LocalPath,
			Status:      domainsync.SyncStatus(p.Status),
			Direction:   domainsync.SyncDirection(p.Direction),
			Alpha:       p.Alpha,
			Beta:        p.Beta,
			MutagenID:   p.MutagenID,
			StartedAt:   p.StartedAt,
			StoppedAt:   p.StoppedAt,
			LastSyncAt:  p.LastSyncAt,
			Stats:       p.Stats,
		}
		st.sessions[sess.ID] = sess
	}

	return nil
}

// Save writes all sessions to disk atomically.
func (st *SessionStore) Save() error {
	st.mu.RLock()
	defer st.mu.RUnlock()

	persisted := make([]persistedSession, 0, len(st.sessions))
	for _, sess := range st.sessions {
		persisted = append(persisted, persistedSession{
			ID:          sess.ID,
			WorkspaceID: sess.WorkspaceID,
			LocalPath:   sess.LocalPath,
			Status:      string(sess.Status),
			Direction:   string(sess.Direction),
			Alpha:       sess.Alpha,
			Beta:        sess.Beta,
			MutagenID:   sess.MutagenID,
			StartedAt:   sess.StartedAt,
			StoppedAt:   sess.StoppedAt,
			LastSyncAt:  sess.LastSyncAt,
			Stats:       sess.Stats,
		})
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("sync: marshal session store: %w", err)
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("sync: create session store directory: %w", err)
	}

	// Write to temp file then rename for atomicity.
	tmpPath := st.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("sync: write session store: %w", err)
	}

	if err := os.Rename(tmpPath, st.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("sync: rename session store: %w", err)
	}

	return nil
}

// Add stores a new session and persists to disk.
func (st *SessionStore) Add(sess *domainsync.SyncSession) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	cp := *sess
	st.sessions[cp.ID] = &cp

	return st.saveLocked()
}

// Remove deletes a session by ID and persists to disk.
func (st *SessionStore) Remove(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	delete(st.sessions, id)
	return st.saveLocked()
}

// Update replaces a stored session and persists to disk.
func (st *SessionStore) Update(sess *domainsync.SyncSession) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	cp := *sess
	st.sessions[cp.ID] = &cp
	return st.saveLocked()
}

// Get retrieves a session by ID.
func (st *SessionStore) Get(id string) (*domainsync.SyncSession, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	sess, ok := st.sessions[id]
	if !ok {
		return nil, false
	}
	cp := *sess
	return &cp, true
}

// List returns all stored sessions.
func (st *SessionStore) List() []*domainsync.SyncSession {
	st.mu.RLock()
	defer st.mu.RUnlock()

	out := make([]*domainsync.SyncSession, 0, len(st.sessions))
	for _, sess := range st.sessions {
		cp := *sess
		out = append(out, &cp)
	}
	return out
}

// saveLocked persists the current sessions to disk. Caller must hold st.mu.
func (st *SessionStore) saveLocked() error {
	persisted := make([]persistedSession, 0, len(st.sessions))
	for _, sess := range st.sessions {
		persisted = append(persisted, persistedSession{
			ID:          sess.ID,
			WorkspaceID: sess.WorkspaceID,
			LocalPath:   sess.LocalPath,
			Status:      string(sess.Status),
			Direction:   string(sess.Direction),
			Alpha:       sess.Alpha,
			Beta:        sess.Beta,
			MutagenID:   sess.MutagenID,
			StartedAt:   sess.StartedAt,
			StoppedAt:   sess.StoppedAt,
			LastSyncAt:  sess.LastSyncAt,
			Stats:       sess.Stats,
		})
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("sync: marshal session store: %w", err)
	}

	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("sync: create session store directory: %w", err)
	}

	tmpPath := st.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("sync: write session store: %w", err)
	}

	if err := os.Rename(tmpPath, st.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("sync: rename session store: %w", err)
	}

	return nil
}
