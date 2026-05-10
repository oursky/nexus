package volume

import (
	"context"
	"errors"
)

var (
	// ErrVolumeNotFound is returned when a volume does not exist.
	ErrVolumeNotFound = errors.New("volume not found")
	// ErrVolumeAlreadyExists is returned when creating a volume with a duplicate name.
	ErrVolumeAlreadyExists = errors.New("volume already exists")
	// ErrVolumeAttached is returned when attempting to delete an attached volume.
	ErrVolumeAttached = errors.New("volume is attached")
	// ErrAttachmentNotFound is returned when an attachment does not exist.
	ErrAttachmentNotFound = errors.New("attachment not found")
	// ErrAttachmentExists is returned when creating a duplicate attachment.
	ErrAttachmentExists = errors.New("attachment already exists")
	// ErrSnapshotNotFound is returned when a snapshot does not exist.
	ErrSnapshotNotFound = errors.New("snapshot not found")
)

// Repository defines storage operations for volumes.
type Repository interface {
	// Volume CRUD
	Create(ctx context.Context, v *Volume) error
	Get(ctx context.Context, id string) (*Volume, error)
	GetByName(ctx context.Context, name string) (*Volume, error)
	Update(ctx context.Context, v *Volume) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filter ListFilter) ([]*Volume, error)

	// Attachment operations
	CreateAttachment(ctx context.Context, a *VolumeAttachment) error
	GetAttachment(ctx context.Context, volumeID, workspaceID string) (*VolumeAttachment, error)
	DeleteAttachment(ctx context.Context, volumeID, workspaceID string) error
	ListAttachmentsByVolume(ctx context.Context, volumeID string) ([]*VolumeAttachment, error)
	ListAttachmentsByWorkspace(ctx context.Context, workspaceID string) ([]*VolumeAttachment, error)

	// Snapshot operations
	CreateSnapshot(ctx context.Context, s *Snapshot) error
	GetSnapshot(ctx context.Context, volumeID, snapshotID string) (*Snapshot, error)
	DeleteSnapshot(ctx context.Context, volumeID, snapshotID string) error
	ListSnapshots(ctx context.Context, volumeID string) ([]*Snapshot, error)
}

// ListFilter filters volume list results.
type ListFilter struct {
	WorkspaceID string
	Type        VolumeType
	State       VolumeState
	Name        string // partial match
}
