package volume

import (
	"context"
	"strings"
	"sync"
	"time"
)

// MemoryRepository is an in-memory implementation of Repository.
type MemoryRepository struct {
	mu          sync.RWMutex
	volumes     map[string]*Volume
	attachments map[string]*VolumeAttachment // key: volumeID+workspaceID
	snapshots   map[string]*Snapshot          // key: volumeID+snapshotID
}

// NewMemoryRepository creates a new in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		volumes:     make(map[string]*Volume),
		attachments: make(map[string]*VolumeAttachment),
		snapshots:   make(map[string]*Snapshot),
	}
}

func attachmentKey(volumeID, workspaceID string) string {
	return volumeID + "::" + workspaceID
}

func snapshotKey(volumeID, snapshotID string) string {
	return volumeID + "::" + snapshotID
}

// Create stores a new volume.
func (r *MemoryRepository) Create(ctx context.Context, v *Volume) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.volumes[v.ID]; exists {
		return ErrVolumeAlreadyExists
	}

	// Check name uniqueness
	for _, existing := range r.volumes {
		if existing.Name == v.Name {
			return ErrVolumeAlreadyExists
		}
	}

	v.CreatedAt = time.Now()
	v.UpdatedAt = v.CreatedAt
	r.volumes[v.ID] = v
	return nil
}

// Get retrieves a volume by ID.
func (r *MemoryRepository) Get(ctx context.Context, id string) (*Volume, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	v, exists := r.volumes[id]
	if !exists {
		return nil, ErrVolumeNotFound
	}
	return copyVolume(v), nil
}

// GetByName retrieves a volume by name.
func (r *MemoryRepository) GetByName(ctx context.Context, name string) (*Volume, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, v := range r.volumes {
		if v.Name == name {
			return copyVolume(v), nil
		}
	}
	return nil, ErrVolumeNotFound
}

// Update modifies an existing volume.
func (r *MemoryRepository) Update(ctx context.Context, v *Volume) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.volumes[v.ID]; !exists {
		return ErrVolumeNotFound
	}

	v.UpdatedAt = time.Now()
	r.volumes[v.ID] = v
	return nil
}

// Delete removes a volume.
func (r *MemoryRepository) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	v, exists := r.volumes[id]
	if !exists {
		return ErrVolumeNotFound
	}

	if v.State != VolumeDetached {
		return ErrVolumeAttached
	}

	delete(r.volumes, id)
	return nil
}

// List returns volumes matching the filter.
func (r *MemoryRepository) List(ctx context.Context, filter ListFilter) ([]*Volume, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Volume
	for _, v := range r.volumes {
		if filter.WorkspaceID != "" && v.WorkspaceID != filter.WorkspaceID {
			continue
		}
		if filter.Type != "" && v.Type != filter.Type {
			continue
		}
		if filter.State != "" && v.State != filter.State {
			continue
		}
		if filter.Name != "" && !strings.Contains(strings.ToLower(v.Name), strings.ToLower(filter.Name)) {
			continue
		}
		result = append(result, copyVolume(v))
	}
	return result, nil
}

// CreateAttachment stores a new attachment.
func (r *MemoryRepository) CreateAttachment(ctx context.Context, a *VolumeAttachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := attachmentKey(a.VolumeID, a.WorkspaceID)
	if _, exists := r.attachments[key]; exists {
		return ErrAttachmentExists
	}

	a.CreatedAt = time.Now()
	r.attachments[key] = a
	return nil
}

// GetAttachment retrieves an attachment.
func (r *MemoryRepository) GetAttachment(ctx context.Context, volumeID, workspaceID string) (*VolumeAttachment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := attachmentKey(volumeID, workspaceID)
	a, exists := r.attachments[key]
	if !exists {
		return nil, ErrAttachmentNotFound
	}
	return copyAttachment(a), nil
}

// DeleteAttachment removes an attachment.
func (r *MemoryRepository) DeleteAttachment(ctx context.Context, volumeID, workspaceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := attachmentKey(volumeID, workspaceID)
	if _, exists := r.attachments[key]; !exists {
		return ErrAttachmentNotFound
	}

	delete(r.attachments, key)
	return nil
}

// ListAttachmentsByVolume returns all attachments for a volume.
func (r *MemoryRepository) ListAttachmentsByVolume(ctx context.Context, volumeID string) ([]*VolumeAttachment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*VolumeAttachment
	for _, a := range r.attachments {
		if a.VolumeID == volumeID {
			result = append(result, copyAttachment(a))
		}
	}
	return result, nil
}

// ListAttachmentsByWorkspace returns all attachments for a workspace.
func (r *MemoryRepository) ListAttachmentsByWorkspace(ctx context.Context, workspaceID string) ([]*VolumeAttachment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*VolumeAttachment
	for _, a := range r.attachments {
		if a.WorkspaceID == workspaceID {
			result = append(result, copyAttachment(a))
		}
	}
	return result, nil
}

// CreateSnapshot stores a new snapshot.
func (r *MemoryRepository) CreateSnapshot(ctx context.Context, s *Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := snapshotKey(s.VolumeID, s.ID)
	if _, exists := r.snapshots[key]; exists {
		return ErrSnapshotNotFound // or a more specific error
	}

	s.CreatedAt = time.Now()
	r.snapshots[key] = s
	return nil
}

// GetSnapshot retrieves a snapshot.
func (r *MemoryRepository) GetSnapshot(ctx context.Context, volumeID, snapshotID string) (*Snapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := snapshotKey(volumeID, snapshotID)
	s, exists := r.snapshots[key]
	if !exists {
		return nil, ErrSnapshotNotFound
	}
	return copySnapshot(s), nil
}

// DeleteSnapshot removes a snapshot.
func (r *MemoryRepository) DeleteSnapshot(ctx context.Context, volumeID, snapshotID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := snapshotKey(volumeID, snapshotID)
	if _, exists := r.snapshots[key]; !exists {
		return ErrSnapshotNotFound
	}

	delete(r.snapshots, key)
	return nil
}

// ListSnapshots returns all snapshots for a volume.
func (r *MemoryRepository) ListSnapshots(ctx context.Context, volumeID string) ([]*Snapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Snapshot
	for _, s := range r.snapshots {
		if s.VolumeID == volumeID {
			result = append(result, copySnapshot(s))
		}
	}
	return result, nil
}

// Helper functions for deep copy

func copyVolume(v *Volume) *Volume {
	if v == nil {
		return nil
	}
	vc := *v
	if len(v.Snapshots) > 0 {
		vc.Snapshots = make([]Snapshot, len(v.Snapshots))
		copy(vc.Snapshots, v.Snapshots)
	}
	return &vc
}

func copyAttachment(a *VolumeAttachment) *VolumeAttachment {
	if a == nil {
		return nil
	}
	ac := *a
	return &ac
}

func copySnapshot(s *Snapshot) *Snapshot {
	if s == nil {
		return nil
	}
	sc := *s
	return &sc
}
