package sync

// SyncStartRequest is the request payload for workspace.sync-start.
type SyncStartRequest struct {
	WorkspaceID string `json:"workspaceId"`
	LocalPath   string `json:"localPath"`
	Direction   string `json:"direction"`
}

// SyncStartResponse is the response for workspace.sync-start.
type SyncStartResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

// SyncStopRequest is the request payload for workspace.sync-stop.
type SyncStopRequest struct {
	SessionID   string `json:"sessionId"`
	WorkspaceID string `json:"workspaceId"`
}

// SyncStopResponse is the response for workspace.sync-stop.
type SyncStopResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// PauseSyncRequest is the request payload for workspace.sync-pause.
type PauseSyncRequest struct {
	SessionID string `json:"sessionId"`
}

// PauseSyncResponse is the response for workspace.sync-pause.
type PauseSyncResponse struct {
	Success bool `json:"success"`
}

// ResumeSyncRequest is the request payload for workspace.sync-resume.
type ResumeSyncRequest struct {
	SessionID string `json:"sessionId"`
}

// ResumeSyncResponse is the response for workspace.sync-resume.
type ResumeSyncResponse struct {
	Success bool `json:"success"`
}

// SyncStatusRequest is the request payload for workspace.sync-status.
type SyncStatusRequest struct {
	SessionID   string `json:"sessionId"`
	WorkspaceID string `json:"workspaceId"`
}

// SyncListRequest is the request payload for workspace.sync-list.
type SyncListRequest struct {
	WorkspaceID string `json:"workspaceId"`
}

// SyncStatsDTO carries per-session sync statistics.
type SyncStatsDTO struct {
	TotalSyncs        int64 `json:"totalSyncs"`
	BytesSent         int64 `json:"bytesSent"`
	BytesReceived     int64 `json:"bytesReceived"`
	FilesSent         int64 `json:"filesSent"`
	FilesReceived     int64 `json:"filesReceived"`
	ConflictsResolved int64 `json:"conflictsResolved"`
}

// SyncStatusResponse is the response for workspace.sync-status and is also
// embedded in the list response.
type SyncStatusResponse struct {
	SessionID   string       `json:"sessionId"`
	WorkspaceID string       `json:"workspaceId"`
	LocalPath   string       `json:"localPath"`
	Status      string       `json:"status"`
	Direction   string       `json:"direction"`
	StartedAt   string       `json:"startedAt"`
	StoppedAt   string       `json:"stoppedAt,omitempty"`
	LastSyncAt  string       `json:"lastSyncAt,omitempty"`
	Stats       SyncStatsDTO `json:"stats"`
}

// SyncListResponse is the response for workspace.sync-list.
type SyncListResponse struct {
	Sessions []SyncStatusResponse `json:"sessions"`
}

// GetSyncStatusRequest is the request payload for workspace.get-sync-status.
type GetSyncStatusRequest struct {
	SessionID   string `json:"sessionId"`
	WorkspaceID string `json:"workspaceId"`
}

// GetSyncProgressRequest is the request payload for workspace.get-sync-progress.
type GetSyncProgressRequest struct {
	SessionID string `json:"sessionId"`
}

// SyncProgressResponse is the response for workspace.get-sync-progress.
type SyncProgressResponse struct {
	SessionID         string `json:"sessionId"`
	Status            string `json:"status"`
	TotalSyncs        int64  `json:"totalSyncs"`
	BytesSent         int64  `json:"bytesSent"`
	BytesReceived     int64  `json:"bytesReceived"`
	FilesSent         int64  `json:"filesSent"`
	FilesReceived     int64  `json:"filesReceived"`
	ConflictsResolved int64  `json:"conflictsResolved"`
}
