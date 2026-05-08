package sync

import "context"

// Repository defines persistence operations for SyncSession entities.
type Repository interface {
	// Create stores a new sync session.
	Create(ctx context.Context, session *SyncSession) error

	// Get retrieves a sync session by ID.
	Get(ctx context.Context, id string) (*SyncSession, error)

	// GetByWorkspace retrieves the active sync session for a workspace.
	GetByWorkspace(ctx context.Context, workspaceID string) (*SyncSession, error)

	// ListByWorkspace lists sync sessions filtered by workspace ID.
	// If workspaceID is empty, returns all sessions.
	ListByWorkspace(ctx context.Context, workspaceID string) ([]*SyncSession, error)

	// Update updates a sync session.
	Update(ctx context.Context, session *SyncSession) error

	// Delete removes a sync session by ID.
	Delete(ctx context.Context, id string) error
}
