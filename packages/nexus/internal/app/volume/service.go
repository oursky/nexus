package volume

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "github.com/oursky/nexus/packages/nexus/internal/domain/volume"
)

// SyncStarter creates and manages sync sessions for volumes.
type SyncStarter interface {
	StartSync(ctx context.Context, workspaceID, localPath, direction string) (sessionID string, err error)
	StartVolumeSync(ctx context.Context, workspaceID, alphaPath, betaPath, direction string) (sessionID string, err error)
	StopSync(ctx context.Context, sessionID, workspaceID string) error
}

// Service provides volume management operations.
type Service struct {
	repo        domain.Repository
	volumeRoot  string // root directory for volume storage
	syncStarter SyncStarter
}

// NewService creates a new volume service.
func NewService(repo domain.Repository, volumeRoot string) *Service {
	return &Service{
		repo:       repo,
		volumeRoot: volumeRoot,
	}
}

// SetSyncStarter wires the sync service after construction.
func (s *Service) SetSyncStarter(starter SyncStarter) {
	s.syncStarter = starter
}

// CreateVolume creates a new volume.
func (s *Service) CreateVolume(ctx context.Context, name string, volType domain.VolumeType, capacity int64, workspaceID string) (*domain.Volume, error) {
	if name == "" {
		return nil, fmt.Errorf("volume name cannot be empty")
	}

	// Check if name already exists
	if _, err := s.repo.GetByName(ctx, name); err == nil {
		return nil, fmt.Errorf("volume '%s' already exists", name)
	}

	vol := &domain.Volume{
		ID:          generateVolumeID(),
		Name:        name,
		Type:        volType,
		State:       domain.VolumeDetached,
		Capacity:    capacity,
		WorkspaceID: workspaceID,
	}

	if err := s.repo.Create(ctx, vol); err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	// Create volume directory on disk
	volPath := s.volumePath(vol.ID)
	if err := os.MkdirAll(filepath.Join(volPath, "data"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create volume directory: %w", err)
	}

	return vol, nil
}

// GetVolume retrieves a volume by ID.
func (s *Service) GetVolume(ctx context.Context, id string) (*domain.Volume, error) {
	return s.repo.Get(ctx, id)
}

// GetVolumeByName retrieves a volume by name.
func (s *Service) GetVolumeByName(ctx context.Context, name string) (*domain.Volume, error) {
	return s.repo.GetByName(ctx, name)
}

// ListVolumes lists volumes matching the filter.
func (s *Service) ListVolumes(ctx context.Context, workspaceID string, volType domain.VolumeType, state domain.VolumeState, name string) ([]*domain.Volume, error) {
	filter := domain.ListFilter{
		WorkspaceID: workspaceID,
		Type:        volType,
		State:       state,
		Name:        name,
	}
	return s.repo.List(ctx, filter)
}

// DeleteVolume deletes a volume.
func (s *Service) DeleteVolume(ctx context.Context, id string, force bool) error {
	vol, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	if !vol.CanDelete() {
		if !force {
			attachments, err := s.repo.ListAttachmentsByVolume(ctx, id)
			if err != nil {
				return err
			}
			return fmt.Errorf("volume is attached to %d workspace(s). Use --force or detach first", len(attachments))
		}
		// Force delete: detach from all workspaces first
		if err := s.DetachVolumeAll(ctx, id); err != nil {
			return fmt.Errorf("failed to detach volume: %w", err)
		}
	}

	// Delete volume directory
	volPath := s.volumePath(id)
	if err := os.RemoveAll(volPath); err != nil {
		return fmt.Errorf("failed to delete volume directory: %w", err)
	}

	return s.repo.Delete(ctx, id)
}

// RenameVolume renames a volume.
func (s *Service) RenameVolume(ctx context.Context, id string, newName string) error {
	if newName == "" {
		return fmt.Errorf("new name cannot be empty")
	}

	// Check if new name already exists
	if existing, err := s.repo.GetByName(ctx, newName); err == nil && existing.ID != id {
		return fmt.Errorf("volume '%s' already exists", newName)
	}

	vol, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	vol.Name = newName
	vol.UpdatedAt = time.Now()
	return s.repo.Update(ctx, vol)
}

// volumePath returns the filesystem path for a volume.
func (s *Service) volumePath(volumeID string) string {
	return filepath.Join(s.volumeRoot, volumeID)
}

// AttachVolume attaches a volume to a workspace.
func (s *Service) AttachVolume(ctx context.Context, volumeID, workspaceID, mountPath string) error {
	if err := domain.ValidateMountPath(mountPath); err != nil {
		return err
	}

	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return err
	}

	if vol.State == domain.VolumeAttached {
		return fmt.Errorf("volume is already attached")
	}

	// Check for mount path conflicts in the same workspace
	existingAttachments, err := s.repo.ListAttachmentsByWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, att := range existingAttachments {
		if att.MountPath == mountPath {
			return fmt.Errorf("mount path '%s' already in use", mountPath)
		}
	}

	attachment := &domain.VolumeAttachment{
		VolumeID:    volumeID,
		WorkspaceID: workspaceID,
		MountPath:   mountPath,
	}

	if err := s.repo.CreateAttachment(ctx, attachment); err != nil {
		return fmt.Errorf("failed to create attachment: %w", err)
	}

	vol.State = domain.VolumeAttached
	vol.MountPath = mountPath
	vol.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, vol); err != nil {
		return err
	}

	// If sync is already enabled, start syncing now that the volume is attached
	if s.syncStarter != nil && vol.Sync != nil && vol.Sync.Enabled {
		dataPath := s.MountPath(volumeID)
		if sessionID, err := s.syncStarter.StartVolumeSync(ctx, workspaceID, vol.Sync.LocalPath, dataPath, vol.Sync.Direction); err != nil {
			log.Printf("volume: failed to auto-start sync on attach for %s: %v", volumeID, err)
		} else {
			vol.Sync.SessionID = sessionID
			vol.Sync.Status = "syncing"
			vol.UpdatedAt = time.Now()
			_ = s.repo.Update(ctx, vol)
		}
	}

	return nil
}

// DetachVolume detaches a volume from a workspace.
func (s *Service) DetachVolume(ctx context.Context, volumeID, workspaceID string) error {
	if _, err := s.repo.GetAttachment(ctx, volumeID, workspaceID); err != nil {
		return err
	}

	if err := s.repo.DeleteAttachment(ctx, volumeID, workspaceID); err != nil {
		return err
	}

	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return err
	}

	// Check if still attached to other workspaces
	attachments, err := s.repo.ListAttachmentsByVolume(ctx, volumeID)
	if err != nil {
		return err
	}

	if len(attachments) == 0 {
		vol.State = domain.VolumeDetached
		vol.MountPath = ""
	}
	vol.UpdatedAt = time.Now()
	return s.repo.Update(ctx, vol)
}

// DetachVolumeAll detaches a volume from all workspaces.
func (s *Service) DetachVolumeAll(ctx context.Context, volumeID string) error {
	attachments, err := s.repo.ListAttachmentsByVolume(ctx, volumeID)
	if err != nil {
		return err
	}

	for _, att := range attachments {
		if err := s.repo.DeleteAttachment(ctx, att.VolumeID, att.WorkspaceID); err != nil {
			return err
		}
	}

	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return err
	}

	vol.State = domain.VolumeDetached
	vol.MountPath = ""
	vol.UpdatedAt = time.Now()
	return s.repo.Update(ctx, vol)
}

// ListVolumeAttachments lists attachments for a volume.
func (s *Service) ListVolumeAttachments(ctx context.Context, volumeID string) ([]*domain.VolumeAttachment, error) {
	return s.repo.ListAttachmentsByVolume(ctx, volumeID)
}

// ListWorkspaceAttachments lists attachments for a workspace.
func (s *Service) ListWorkspaceAttachments(ctx context.Context, workspaceID string) ([]*domain.VolumeAttachment, error) {
	return s.repo.ListAttachmentsByWorkspace(ctx, workspaceID)
}

// CreateSnapshot creates a snapshot of a volume.
func (s *Service) CreateSnapshot(ctx context.Context, volumeID string, name string) (*domain.Snapshot, error) {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return nil, err
	}

	if vol.State == domain.VolumeAttached {
		return nil, fmt.Errorf("cannot snapshot an attached volume")
	}

	snapshot := &domain.Snapshot{
		ID:        generateSnapshotID(),
		Name:      name,
		VolumeID:  volumeID,
		CreatedAt: time.Now(),
		Size:      vol.Size,
	}

	vol.Snapshots = append(vol.Snapshots, *snapshot)
	vol.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, vol); err != nil {
		return nil, err
	}

	// Create snapshot directory by copying volume data
	srcPath := filepath.Join(s.volumePath(volumeID), "data")
	dstPath := filepath.Join(s.volumePath(volumeID), "snapshots", snapshot.ID)
	if err := os.MkdirAll(dstPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create snapshot directory: %w", err)
	}
	if err := copyDir(srcPath, dstPath); err != nil {
		return nil, fmt.Errorf("failed to copy volume data: %w", err)
	}

	return snapshot, nil
}

// ListSnapshots lists snapshots for a volume.
func (s *Service) ListSnapshots(ctx context.Context, volumeID string) ([]domain.Snapshot, error) {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return nil, err
	}
	return vol.Snapshots, nil
}

// RestoreSnapshot restores a volume from a snapshot.
func (s *Service) RestoreSnapshot(ctx context.Context, volumeID string, snapshotID string) error {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return err
	}

	if vol.State == domain.VolumeAttached {
		return fmt.Errorf("cannot restore an attached volume")
	}

	var snapshot *domain.Snapshot
	for _, snap := range vol.Snapshots {
		if snap.ID == snapshotID {
			snapshot = &snap
			break
		}
	}
	if snapshot == nil {
		return fmt.Errorf("snapshot not found")
	}

	// Restore volume data from snapshot
	srcPath := filepath.Join(s.volumePath(volumeID), "snapshots", snapshot.ID)
	dstPath := filepath.Join(s.volumePath(volumeID), "data")
	if err := os.RemoveAll(dstPath); err != nil {
		return fmt.Errorf("failed to remove existing data: %w", err)
	}
	if err := copyDir(srcPath, dstPath); err != nil {
		return fmt.Errorf("failed to restore snapshot: %w", err)
	}

	vol.UpdatedAt = time.Now()
	return s.repo.Update(ctx, vol)
}

// DeleteSnapshot deletes a snapshot.
func (s *Service) DeleteSnapshot(ctx context.Context, volumeID string, snapshotID string) error {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return err
	}

	var found bool
	var newSnapshots []domain.Snapshot
	for _, snap := range vol.Snapshots {
		if snap.ID == snapshotID {
			found = true
			// Remove snapshot directory
			snapPath := filepath.Join(s.volumePath(volumeID), "snapshots", snap.ID)
			if err := os.RemoveAll(snapPath); err != nil {
				return fmt.Errorf("failed to remove snapshot directory: %w", err)
			}
			continue
		}
		newSnapshots = append(newSnapshots, snap)
	}
	if !found {
		return fmt.Errorf("snapshot not found")
	}

	vol.Snapshots = newSnapshots
	vol.UpdatedAt = time.Now()
	return s.repo.Update(ctx, vol)
}

func generateVolumeID() string {
	return "vol-" + uuid.New().String()
}

func generateSnapshotID() string {
	return "snap-" + uuid.New().String()
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

// MountPath returns the filesystem path for a volume's data directory.
// This implements the workspace.VolumeMounter interface.
func (s *Service) MountPath(volumeID string) string {
	return filepath.Join(s.volumePath(volumeID), "data")
}

// EnableSync enables sync for a volume with a local directory.
func (s *Service) EnableSync(ctx context.Context, volumeID string, localPath string, direction string) (*domain.Volume, error) {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return nil, fmt.Errorf("volume not found: %w", err)
	}

	if vol.Sync != nil && vol.Sync.Enabled {
		return nil, fmt.Errorf("sync already enabled for volume %s", volumeID)
	}

	// Skip stat check for remote paths (SSH URLs or SCP-style)
	if !strings.HasPrefix(localPath, "ssh://") && !strings.Contains(localPath, "@") {
		if _, err := os.Stat(localPath); err != nil {
			return nil, fmt.Errorf("local path not accessible: %w", err)
		}
	}

	vol.Sync = &domain.SyncConfig{
		Enabled:   true,
		LocalPath: localPath,
		Direction: direction,
		Status:    "idle",
	}
	vol.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, vol); err != nil {
		return nil, fmt.Errorf("failed to update volume: %w", err)
	}

	// If sync starter is available and volume is attached to a workspace, start sync now
	if s.syncStarter != nil && vol.State == domain.VolumeAttached {
		// Get the workspace this volume is attached to
		attachments, err := s.repo.ListAttachmentsByVolume(ctx, volumeID)
		if err == nil && len(attachments) > 0 {
			workspaceID := attachments[0].WorkspaceID
			dataPath := s.MountPath(volumeID)
			if sessionID, err := s.syncStarter.StartVolumeSync(ctx, workspaceID, localPath, dataPath, direction); err != nil {
				// Log error but don't fail - sync can be started later
				log.Printf("volume: failed to start sync for %s: %v", volumeID, err)
			} else {
				vol.Sync.SessionID = sessionID
				vol.Sync.Status = "syncing"
				vol.UpdatedAt = time.Now()
				_ = s.repo.Update(ctx, vol)
			}
		}
	}

	return vol, nil
}

// DisableSync disables sync for a volume.
func (s *Service) DisableSync(ctx context.Context, volumeID string) (*domain.Volume, error) {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return nil, fmt.Errorf("volume not found: %w", err)
	}

	if vol.Sync == nil || !vol.Sync.Enabled {
		return nil, fmt.Errorf("sync not enabled for volume %s", volumeID)
	}

	vol.Sync = nil
	vol.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, vol); err != nil {
		return nil, fmt.Errorf("failed to update volume: %w", err)
	}

	return vol, nil
}

// StartVolumeSync starts sync for a volume attached to a workspace.
// This is called by the workspace service when a workspace starts.
func (s *Service) StartVolumeSync(ctx context.Context, workspaceID, volumeID string) (string, error) {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return "", fmt.Errorf("volume not found: %w", err)
	}
	if vol.Sync == nil || !vol.Sync.Enabled {
		return "", fmt.Errorf("sync not enabled for volume %s", volumeID)
	}
	if s.syncStarter == nil {
		return "", fmt.Errorf("sync starter not available")
	}
	dataPath := s.MountPath(volumeID)
	sessionID, err := s.syncStarter.StartVolumeSync(ctx, workspaceID, vol.Sync.LocalPath, dataPath, vol.Sync.Direction)
	if err != nil {
		return "", fmt.Errorf("failed to start sync: %w", err)
	}
	vol.Sync.SessionID = sessionID
	vol.Sync.Status = "syncing"
	vol.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, vol); err != nil {
		return "", fmt.Errorf("failed to update volume: %w", err)
	}
	return sessionID, nil
}

// StopVolumeSync stops sync for a volume.
// This is called by the workspace service when a workspace stops.
func (s *Service) StopVolumeSync(ctx context.Context, volumeID string) error {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return fmt.Errorf("volume not found: %w", err)
	}
	if vol.Sync == nil || !vol.Sync.Enabled || vol.Sync.SessionID == "" {
		return nil
	}
	if s.syncStarter != nil {
		if err := s.syncStarter.StopSync(ctx, vol.Sync.SessionID, ""); err != nil {
			log.Printf("volume: failed to stop sync for %s: %v", volumeID, err)
		}
	}
	vol.Sync.SessionID = ""
	vol.Sync.Status = "idle"
	vol.UpdatedAt = time.Now()
	return s.repo.Update(ctx, vol)
}

// GetSyncConfig returns the sync configuration for a volume.
func (s *Service) GetSyncConfig(ctx context.Context, volumeID string) (*domain.SyncConfig, error) {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return nil, fmt.Errorf("volume not found: %w", err)
	}

	if vol.Sync == nil {
		return nil, fmt.Errorf("sync not configured for volume %s", volumeID)
	}

	return vol.Sync, nil
}

// UpdateSyncSession updates the sync session ID for a volume.
func (s *Service) UpdateSyncSession(ctx context.Context, volumeID string, sessionID string, status string) error {
	vol, err := s.repo.Get(ctx, volumeID)
	if err != nil {
		return fmt.Errorf("volume not found: %w", err)
	}

	if vol.Sync == nil {
		return fmt.Errorf("sync not configured for volume %s", volumeID)
	}

	vol.Sync.SessionID = sessionID
	vol.Sync.Status = status
	now := time.Now()
	vol.Sync.LastSyncAt = &now
	vol.UpdatedAt = now

	return s.repo.Update(ctx, vol)
}

// Mount creates a persistent volume from a local path, attaches it to a workspace, and enables sync.
func (s *Service) Mount(ctx context.Context, localPath, workspaceID, mountPath, direction string) (*domain.Volume, error) {
	name := filepath.Base(localPath)
	if mountPath == "" {
		mountPath = filepath.Join("/workspace/volumes", name)
	}
	if direction == "" {
		direction = "bidirectional"
	}

	vol, err := s.CreateVolume(ctx, name, domain.VolumePersistent, 0, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	if err := s.AttachVolume(ctx, vol.ID, workspaceID, mountPath); err != nil {
		return nil, fmt.Errorf("failed to attach volume: %w", err)
	}

	if _, err := s.EnableSync(ctx, vol.ID, localPath, direction); err != nil {
		return nil, fmt.Errorf("failed to enable sync: %w", err)
	}

	return s.GetVolume(ctx, vol.ID)
}

// Unmount stops sync, detaches a volume from a workspace, and optionally deletes it.
func (s *Service) Unmount(ctx context.Context, volumeID, workspaceID string, delete bool) error {
	if err := s.StopVolumeSync(ctx, volumeID); err != nil {
		return fmt.Errorf("failed to stop sync: %w", err)
	}

	if err := s.DetachVolume(ctx, volumeID, workspaceID); err != nil {
		return fmt.Errorf("failed to detach volume: %w", err)
	}

	if delete {
		if err := s.DeleteVolume(ctx, volumeID, false); err != nil {
			return fmt.Errorf("failed to delete volume: %w", err)
		}
	}

	return nil
}
