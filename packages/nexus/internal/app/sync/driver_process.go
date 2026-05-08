package sync

import (
	"context"
	"fmt"
)

// ProcessDriver implements SyncDriver for process sandbox workspaces.
// Alpha is the local sync directory; beta is the workspace root path (local filesystem).
type ProcessDriver struct {
	wsLookup WorkspaceLookup
}

// NewProcessDriver creates a new ProcessDriver.
func NewProcessDriver(wsLookup WorkspaceLookup) *ProcessDriver {
	return &ProcessDriver{wsLookup: wsLookup}
}

// IsAvailable returns true if the workspace uses the sandbox (process) backend.
func (d *ProcessDriver) IsAvailable(workspaceID string) bool {
	ws, err := d.wsLookup.Get(context.Background(), workspaceID)
	if err != nil {
		return false
	}
	// Process sandbox: backend is "process", "sandbox", or empty.
	return ws.Backend == "process" || ws.Backend == "sandbox" || ws.Backend == ""
}

// GetSyncPaths returns alpha (local) and beta (workspace root) paths.
func (d *ProcessDriver) GetSyncPaths(workspaceID string) (alpha, beta string, err error) {
	ws, err := d.wsLookup.Get(context.Background(), workspaceID)
	if err != nil {
		return "", "", fmt.Errorf("process driver: workspace %q not found: %w", workspaceID, err)
	}
	if ws.RootPath == "" {
		return "", "", fmt.Errorf("process driver: workspace %q has no root path", workspaceID)
	}
	// Alpha is the local sync directory (caller provides this).
	// Beta is the workspace root path on the local filesystem.
	return "", ws.RootPath, nil
}
