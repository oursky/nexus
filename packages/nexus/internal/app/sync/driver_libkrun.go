package sync

import (
	"context"
	"fmt"
)

// LibkrunDriver implements SyncDriver for libkrun VM workspaces.
// Alpha is the local sync directory; beta is an SSH path to the VM.
type LibkrunDriver struct {
	wsLookup WorkspaceLookup
}

// NewLibkrunDriver creates a new LibkrunDriver.
func NewLibkrunDriver(wsLookup WorkspaceLookup) *LibkrunDriver {
	return &LibkrunDriver{wsLookup: wsLookup}
}

// IsAvailable returns true if the workspace uses the libkrun backend.
func (d *LibkrunDriver) IsAvailable(workspaceID string) bool {
	ws, err := d.wsLookup.Get(context.Background(), workspaceID)
	if err != nil {
		return false
	}
	return ws.Backend == "libkrun"
}

// GetSyncPaths returns alpha (local) and beta (SSH path) for the workspace.
func (d *LibkrunDriver) GetSyncPaths(workspaceID string) (alpha, beta string, err error) {
	ws, err := d.wsLookup.Get(context.Background(), workspaceID)
	if err != nil {
		return "", "", fmt.Errorf("libkrun driver: workspace %q not found: %w", workspaceID, err)
	}
	if ws.RootPath == "" {
		return "", "", fmt.Errorf("libkrun driver: workspace %q has no root path", workspaceID)
	}
	if ws.GuestIP == "" {
		return "", "", fmt.Errorf("libkrun driver: workspace %q has no guest IP (VM not running)", workspaceID)
	}
	// Alpha is the local sync directory (caller provides this).
	// Beta is an SSH path to the VM guest.
	beta = fmt.Sprintf("user@%s:%s", ws.GuestIP, ws.RootPath)
	return "", beta, nil
}
