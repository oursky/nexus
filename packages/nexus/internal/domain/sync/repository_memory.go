package sync

import (
	"context"
	"fmt"
	"sync"
)

// MemoryRepository is an in-memory implementation of Repository.
type MemoryRepository struct {
	mu       sync.RWMutex
	sessions map[string]*SyncSession
}

// NewMemoryRepository creates a new MemoryRepository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		sessions: make(map[string]*SyncSession),
	}
}

// Create stores a new sync session.
func (r *MemoryRepository) Create(_ context.Context, session *SyncSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Store a copy to avoid aliasing.
	cp := *session
	r.sessions[cp.ID] = &cp
	return nil
}

// Get retrieves a sync session by ID.
func (r *MemoryRepository) Get(_ context.Context, id string) (*SyncSession, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("sync session %q not found", id)
	}
	cp := *s
	return &cp, nil
}

// GetByWorkspace returns the first active (non-stopped) session for the workspace.
func (r *MemoryRepository) GetByWorkspace(_ context.Context, workspaceID string) (*SyncSession, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, s := range r.sessions {
		if s.WorkspaceID == workspaceID && s.Status != SyncStatusStopped {
			cp := *s
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("no active sync session for workspace %q", workspaceID)
}

// ListByWorkspace returns sessions filtered by workspaceID; if empty, returns all.
func (r *MemoryRepository) ListByWorkspace(_ context.Context, workspaceID string) ([]*SyncSession, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []*SyncSession
	for _, s := range r.sessions {
		if workspaceID == "" || s.WorkspaceID == workspaceID {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}

// Update replaces a stored session with the provided value.
func (r *MemoryRepository) Update(_ context.Context, session *SyncSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.sessions[session.ID]; !ok {
		return fmt.Errorf("sync session %q not found", session.ID)
	}
	cp := *session
	r.sessions[cp.ID] = &cp
	return nil
}

// Delete removes a sync session by ID.
func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.sessions[id]; !ok {
		return fmt.Errorf("sync session %q not found", id)
	}
	delete(r.sessions, id)
	return nil
}
