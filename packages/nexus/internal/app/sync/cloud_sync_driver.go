package sync

import (
	"context"
	"fmt"
)

// CloudSyncDriver implements SyncDriver for cloud-backed workspaces.
// Beta is an S3 URI pointing to the workspace's cloud storage.
type CloudSyncDriver struct {
	wsLookup WorkspaceLookup
	bucket   string
}

// NewCloudSyncDriver creates a new CloudSyncDriver.
func NewCloudSyncDriver(wsLookup WorkspaceLookup, bucket string) *CloudSyncDriver {
	return &CloudSyncDriver{wsLookup: wsLookup, bucket: bucket}
}

// IsAvailable returns true if the workspace uses the cloud backend.
func (d *CloudSyncDriver) IsAvailable(workspaceID string) bool {
	ws, err := d.wsLookup.Get(context.Background(), workspaceID)
	if err != nil {
		return false
	}
	return ws.Backend == "cloud"
}

// GetSyncPaths returns alpha (local) and beta (S3 URI) for the workspace.
func (d *CloudSyncDriver) GetSyncPaths(workspaceID string) (alpha, beta string, err error) {
	ws, err := d.wsLookup.Get(context.Background(), workspaceID)
	if err != nil {
		return "", "", fmt.Errorf("cloud driver: workspace %q not found: %w", workspaceID, err)
	}
	if ws.RootPath == "" {
		return "", "", fmt.Errorf("cloud driver: workspace %q has no root path", workspaceID)
	}
	// Alpha is the local sync directory (caller provides this).
	// Beta is an S3 URI: s3://bucket/workspaces/{workspaceID}{rootPath}
	beta = fmt.Sprintf("s3://%s/workspaces/%s%s", d.bucket, workspaceID, ws.RootPath)
	return "", beta, nil
}
