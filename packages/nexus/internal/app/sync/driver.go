package sync

// SyncDriver determines alpha (local) and beta (remote) sync paths for a workspace.
type SyncDriver interface {
	// GetSyncPaths returns alpha (local) and beta (remote) paths for a workspace.
	GetSyncPaths(workspaceID string) (alpha, beta string, err error)
	// IsAvailable returns true if this driver can handle the workspace.
	IsAvailable(workspaceID string) bool
}
