package volume

import (
	"time"
)

// VolumeType defines the persistence mode of a volume.
type VolumeType string

const (
	// VolumePersistent is independent of workspace lifecycle.
	VolumePersistent VolumeType = "persistent"
	// VolumeEphemeral is created with workspace and deleted on stop.
	VolumeEphemeral VolumeType = "ephemeral"
	// VolumeWorkspaceBound is tied to workspace but survives stop/start.
	VolumeWorkspaceBound VolumeType = "workspace-bound"
)

// VolumeState represents the attachment state of a volume.
type VolumeState string

const (
	VolumeDetached  VolumeState = "detached"
	VolumeAttaching VolumeState = "attaching"
	VolumeAttached  VolumeState = "attached"
	VolumeDetaching VolumeState = "detaching"
)

// SyncConfig holds optional sync configuration for a volume.
type SyncConfig struct {
	Enabled     bool   `json:"enabled"`
	LocalPath   string `json:"localPath,omitempty"`   // local directory to sync with
	SessionID   string `json:"sessionId,omitempty"`   // active mutagen session ID
	Direction   string `json:"direction,omitempty"`   // up, down, bidirectional
	LastSyncAt  *time.Time `json:"lastSyncAt,omitempty"`
	Status      string `json:"status,omitempty"`      // idle, syncing, error
}

// Volume is the core domain entity for managed storage.
type Volume struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Type        VolumeType  `json:"type"`
	State       VolumeState `json:"state"`
	Size        int64       `json:"size"`     // bytes used (best effort)
	Capacity    int64       `json:"capacity"` // max bytes (advisory in v1)
	WorkspaceID string      `json:"workspaceId,omitempty"` // for workspace-bound volumes
	MountPath   string      `json:"mountPath,omitempty"`   // current mount point
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
	Snapshots   []Snapshot  `json:"snapshots,omitempty"`
	Sync        *SyncConfig `json:"sync,omitempty"`        // optional sync configuration
}

// CanDelete reports whether the volume can be safely deleted.
func (v *Volume) CanDelete() bool {
	return v.State == VolumeDetached
}

// IsAttached reports whether the volume is currently attached to a workspace.
func (v *Volume) IsAttached() bool {
	return v.State == VolumeAttached || v.State == VolumeAttaching
}

// Snapshot represents a point-in-time copy of a volume.
type Snapshot struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	VolumeID  string    `json:"volumeId"`
	CreatedAt time.Time `json:"createdAt"`
	Size      int64     `json:"size"`
}

// VolumeAttachment links a volume to a workspace at a specific mount path.
type VolumeAttachment struct {
	VolumeID    string    `json:"volumeId"`
	WorkspaceID string    `json:"workspaceId"`
	MountPath   string    `json:"mountPath"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ValidateMountPath checks if a mount path is valid.
func ValidateMountPath(path string) error {
	if path == "" {
		return ErrInvalidMountPath("mount path cannot be empty")
	}
	if path[0] != '/' {
		return ErrInvalidMountPath("mount path must be absolute (e.g., /data)")
	}
	if path == "/" || path == "/workspace" {
		return ErrInvalidMountPath("mount path cannot be / or /workspace")
	}
	return nil
}

// ErrInvalidMountPath is returned when a mount path is invalid.
type ErrInvalidMountPath string

func (e ErrInvalidMountPath) Error() string {
	return string(e)
}
