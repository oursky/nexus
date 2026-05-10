package volume

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	domain "github.com/oursky/nexus/packages/nexus/internal/domain/volume"
)

func setupTestService(t *testing.T) (*Service, string) {
	t.Helper()
	tmpDir := t.TempDir()
	repo := domain.NewMemoryRepository()
	return NewService(repo, filepath.Join(tmpDir, "volumes")), tmpDir
}

func TestCreateVolume(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	vol, err := svc.CreateVolume(ctx, "mydata", domain.VolumePersistent, 1024*1024*1024, "")
	if err != nil {
		t.Fatalf("create volume: %v", err)
	}
	if vol.Name != "mydata" {
		t.Errorf("name = %q, want mydata", vol.Name)
	}
	if vol.Type != domain.VolumePersistent {
		t.Errorf("type = %q, want persistent", vol.Type)
	}
	if vol.State != domain.VolumeDetached {
		t.Errorf("state = %q, want detached", vol.State)
	}

	// Verify directory was created
	volPath := filepath.Join(svc.volumeRoot, vol.ID, "data")
	if _, err := os.Stat(volPath); os.IsNotExist(err) {
		t.Errorf("volume directory not created at %s", volPath)
	}
}

func TestCreateVolumeDuplicateName(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	_, err := svc.CreateVolume(ctx, "mydata", domain.VolumePersistent, 0, "")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = svc.CreateVolume(ctx, "mydata", domain.VolumeEphemeral, 0, "")
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestGetVolume(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	created, _ := svc.CreateVolume(ctx, "mydata", domain.VolumePersistent, 0, "")

	vol, err := svc.GetVolume(ctx, created.ID)
	if err != nil {
		t.Fatalf("get volume: %v", err)
	}
	if vol.ID != created.ID {
		t.Errorf("id = %q, want %q", vol.ID, created.ID)
	}

	_, err = svc.GetVolume(ctx, "nonexistent")
	if err != domain.ErrVolumeNotFound {
		t.Errorf("expected ErrVolumeNotFound, got %v", err)
	}
}

func TestDeleteVolume(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	vol, _ := svc.CreateVolume(ctx, "mydata", domain.VolumePersistent, 0, "")

	if err := svc.DeleteVolume(ctx, vol.ID, false); err != nil {
		t.Fatalf("delete volume: %v", err)
	}

	_, err := svc.GetVolume(ctx, vol.ID)
	if err != domain.ErrVolumeNotFound {
		t.Errorf("expected ErrVolumeNotFound after delete, got %v", err)
	}

	// Verify directory was removed
	volPath := filepath.Join(svc.volumeRoot, vol.ID)
	if _, err := os.Stat(volPath); !os.IsNotExist(err) {
		t.Errorf("volume directory should be removed at %s", volPath)
	}
}

func TestDeleteAttachedVolume(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	vol, _ := svc.CreateVolume(ctx, "mydata", domain.VolumePersistent, 0, "")
	svc.AttachVolume(ctx, vol.ID, "ws-123", "/data")

	// Should fail without force
	err := svc.DeleteVolume(ctx, vol.ID, false)
	if err == nil {
		t.Fatal("expected error deleting attached volume without force")
	}

	// Should succeed with force
	if err := svc.DeleteVolume(ctx, vol.ID, true); err != nil {
		t.Fatalf("delete with force: %v", err)
	}
}

func TestAttachVolume(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	vol, _ := svc.CreateVolume(ctx, "mydata", domain.VolumePersistent, 0, "")

	if err := svc.AttachVolume(ctx, vol.ID, "ws-123", "/data"); err != nil {
		t.Fatalf("attach volume: %v", err)
	}

	// Verify state
	updated, _ := svc.GetVolume(ctx, vol.ID)
	if updated.State != domain.VolumeAttached {
		t.Errorf("state = %q, want attached", updated.State)
	}
	if updated.MountPath != "/data" {
		t.Errorf("mountPath = %q, want /data", updated.MountPath)
	}

	// Verify attachment
	attachments, _ := svc.ListVolumeAttachments(ctx, vol.ID)
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].WorkspaceID != "ws-123" {
		t.Errorf("workspaceID = %q, want ws-123", attachments[0].WorkspaceID)
	}
}

func TestAttachVolumeConflict(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	vol1, _ := svc.CreateVolume(ctx, "data1", domain.VolumePersistent, 0, "")
	vol2, _ := svc.CreateVolume(ctx, "data2", domain.VolumePersistent, 0, "")

	svc.AttachVolume(ctx, vol1.ID, "ws-123", "/data")

	// Same mount path should conflict
	err := svc.AttachVolume(ctx, vol2.ID, "ws-123", "/data")
	if err == nil {
		t.Fatal("expected error for mount path conflict")
	}
}

func TestDetachVolume(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	vol, _ := svc.CreateVolume(ctx, "mydata", domain.VolumePersistent, 0, "")
	svc.AttachVolume(ctx, vol.ID, "ws-123", "/data")

	if err := svc.DetachVolume(ctx, vol.ID, "ws-123"); err != nil {
		t.Fatalf("detach volume: %v", err)
	}

	// Verify state
	updated, _ := svc.GetVolume(ctx, vol.ID)
	if updated.State != domain.VolumeDetached {
		t.Errorf("state = %q, want detached", updated.State)
	}

	// Verify attachment removed
	attachments, _ := svc.ListVolumeAttachments(ctx, vol.ID)
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(attachments))
	}
}

func TestListVolumes(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	svc.CreateVolume(ctx, "vol1", domain.VolumePersistent, 0, "")
	svc.CreateVolume(ctx, "vol2", domain.VolumeEphemeral, 0, "ws-123")
	svc.CreateVolume(ctx, "vol3", domain.VolumePersistent, 0, "ws-123")

	// List all
	all, _ := svc.ListVolumes(ctx, "", "", "", "")
	if len(all) != 3 {
		t.Errorf("expected 3 volumes, got %d", len(all))
	}

	// Filter by workspace
	wsVolumes, _ := svc.ListVolumes(ctx, "ws-123", "", "", "")
	if len(wsVolumes) != 2 {
		t.Errorf("expected 2 workspace volumes, got %d", len(wsVolumes))
	}

	// Filter by type
	persistent, _ := svc.ListVolumes(ctx, "", domain.VolumePersistent, "", "")
	if len(persistent) != 2 {
		t.Errorf("expected 2 persistent volumes, got %d", len(persistent))
	}
}
