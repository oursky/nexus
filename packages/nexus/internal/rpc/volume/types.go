package volume

// CreateRequest represents a volume creation request.
type CreateRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Capacity    int64  `json:"capacity,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
}

// CreateResponse represents a volume creation response.
type CreateResponse struct {
	Volume *VolumeDTO `json:"volume"`
}

// DeleteRequest represents a volume deletion request.
type DeleteRequest struct {
	VolumeID string `json:"volumeId"`
	Force    bool   `json:"force,omitempty"`
}

// ListRequest represents a volume list request.
type ListRequest struct {
	WorkspaceID string `json:"workspaceId,omitempty"`
	Type        string `json:"type,omitempty"`
	State       string `json:"state,omitempty"`
	Name        string `json:"name,omitempty"`
}

// ListResponse represents a volume list response.
type ListResponse struct {
	Volumes []*VolumeDTO `json:"volumes"`
}

// InfoRequest represents a volume info request.
type InfoRequest struct {
	VolumeID string `json:"volumeId"`
}

// InfoResponse represents a volume info response.
type InfoResponse struct {
	Volume *VolumeDTO `json:"volume"`
}

// RenameRequest represents a volume rename request.
type RenameRequest struct {
	VolumeID string `json:"volumeId"`
	NewName  string `json:"newName"`
}

// AttachRequest represents a volume attach request.
type AttachRequest struct {
	VolumeID    string `json:"volumeId"`
	WorkspaceID string `json:"workspaceId"`
	MountPath   string `json:"mountPath"`
}

// DetachRequest represents a volume detach request.
type DetachRequest struct {
	VolumeID    string `json:"volumeId"`
	WorkspaceID string `json:"workspaceId"`
}

// VolumeDTO is the data transfer object for volumes.
type VolumeDTO struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Type        string           `json:"type"`
	State       string           `json:"state"`
	Size        int64            `json:"size"`
	Capacity    int64            `json:"capacity"`
	WorkspaceID string           `json:"workspaceId,omitempty"`
	MountPath   string           `json:"mountPath,omitempty"`
	CreatedAt   string           `json:"createdAt"`
	UpdatedAt   string           `json:"updatedAt"`
	Attachments []*AttachmentDTO `json:"attachments,omitempty"`
}

// AttachmentDTO is the data transfer object for attachments.
type AttachmentDTO struct {
	VolumeID    string `json:"volumeId"`
	WorkspaceID string `json:"workspaceId"`
	MountPath   string `json:"mountPath"`
	CreatedAt   string `json:"createdAt"`
}

// SnapshotCreateRequest represents a snapshot creation request.
type SnapshotCreateRequest struct {
	VolumeID string `json:"volumeId"`
	Name     string `json:"name"`
}

// SnapshotCreateResponse represents a snapshot creation response.
type SnapshotCreateResponse struct {
	Snapshot *SnapshotDTO `json:"snapshot"`
}

// SnapshotListRequest represents a snapshot list request.
type SnapshotListRequest struct {
	VolumeID string `json:"volumeId"`
}

// SnapshotListResponse represents a snapshot list response.
type SnapshotListResponse struct {
	Snapshots []*SnapshotDTO `json:"snapshots"`
}

// SnapshotRestoreRequest represents a snapshot restore request.
type SnapshotRestoreRequest struct {
	VolumeID   string `json:"volumeId"`
	SnapshotID string `json:"snapshotId"`
}

// SnapshotDeleteRequest represents a snapshot delete request.
type SnapshotDeleteRequest struct {
	VolumeID   string `json:"volumeId"`
	SnapshotID string `json:"snapshotId"`
}

// SnapshotDTO is the data transfer object for snapshots.
type SnapshotDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	VolumeID  string `json:"volumeId"`
	CreatedAt string `json:"createdAt"`
	Size      int64  `json:"size"`
}

// SyncEnableRequest represents a sync enable request.
type SyncEnableRequest struct {
	VolumeID  string `json:"volumeId"`
	LocalPath string `json:"localPath"`
	Direction string `json:"direction,omitempty"`
}

// SyncDisableRequest represents a sync disable request.
type SyncDisableRequest struct {
	VolumeID string `json:"volumeId"`
}

// SyncInfoRequest represents a sync info request.
type SyncInfoRequest struct {
	VolumeID string `json:"volumeId"`
}

// SyncInfoResponse represents a sync info response.
type SyncInfoResponse struct {
	Sync *SyncConfigDTO `json:"sync"`
}

// SyncConfigDTO is the data transfer object for sync configuration.
type SyncConfigDTO struct {
	Enabled    bool   `json:"enabled"`
	LocalPath  string `json:"localPath,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	Direction  string `json:"direction,omitempty"`
	Status     string `json:"status,omitempty"`
	LastSyncAt string `json:"lastSyncAt,omitempty"`
}

// MountRequest represents a volume mount request.
type MountRequest struct {
	LocalPath   string `json:"localPath"`
	WorkspaceID string `json:"workspaceId"`
	MountPath   string `json:"mountPath,omitempty"`
	Direction   string `json:"direction,omitempty"`
}

// MountResponse represents a volume mount response.
type MountResponse struct {
	Volume *VolumeDTO `json:"volume"`
}

// UnmountRequest represents a volume unmount request.
type UnmountRequest struct {
	VolumeID    string `json:"volumeId"`
	WorkspaceID string `json:"workspaceId"`
	Delete      bool   `json:"delete,omitempty"`
}
