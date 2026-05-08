package volume

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	service "github.com/oursky/nexus/packages/nexus/internal/app/volume"
	domain "github.com/oursky/nexus/packages/nexus/internal/domain/volume"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

// json.RawMessage is defined in encoding/json
var _ = json.RawMessage(nil)

// Handler handles volume RPC requests.
type Handler struct {
	svc *service.Service
}

// NewHandler creates a new volume handler.
func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// Register registers volume methods with the registry.
func (h *Handler) Register(r registry.Registry) {
	r.Register("volume.create", h.handleCreate)
	r.Register("volume.delete", h.handleDelete)
	r.Register("volume.list", h.handleList)
	r.Register("volume.info", h.handleInfo)
	r.Register("volume.rename", h.handleRename)
	r.Register("volume.attach", h.handleAttach)
	r.Register("volume.detach", h.handleDetach)
	r.Register("volume.detachAll", h.handleDetachAll)
	r.Register("volume.snapshot.create", h.handleSnapshotCreate)
	r.Register("volume.snapshot.list", h.handleSnapshotList)
	r.Register("volume.snapshot.restore", h.handleSnapshotRestore)
	r.Register("volume.snapshot.delete", h.handleSnapshotDelete)
	r.Register("volume.sync.enable", h.handleSyncEnable)
	r.Register("volume.sync.disable", h.handleSyncDisable)
	r.Register("volume.sync.info", h.handleSyncInfo)
	r.Register("volume.mount", h.handleMount)
	r.Register("volume.unmount", h.handleUnmount)
}

func (h *Handler) handleCreate(ctx context.Context, params json.RawMessage) (any, error) {
	var req CreateRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	volType := domain.VolumeType(req.Type)
	if volType == "" {
		volType = domain.VolumePersistent
	}

	vol, err := h.svc.CreateVolume(ctx, req.Name, volType, req.Capacity, req.WorkspaceID)
	if err != nil {
		return nil, err
	}

	return &CreateResponse{Volume: toVolumeDTO(vol)}, nil
}

func (h *Handler) handleDelete(ctx context.Context, params json.RawMessage) (any, error) {
	var req DeleteRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" {
		return nil, fmt.Errorf("volumeId is required")
	}

	volID := req.VolumeID
	if _, err := h.svc.GetVolume(ctx, volID); err != nil {
		// Try lookup by name
		vol, err := h.svc.GetVolumeByName(ctx, volID)
		if err == nil {
			volID = vol.ID
		}
	}

	if err := h.svc.DeleteVolume(ctx, volID, req.Force); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true}, nil
}

func (h *Handler) handleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req ListRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
	}

	volumes, err := h.svc.ListVolumes(ctx, req.WorkspaceID, domain.VolumeType(req.Type), domain.VolumeState(req.State), req.Name)
	if err != nil {
		return nil, err
	}

	var dtos []*VolumeDTO
	for _, vol := range volumes {
		dtos = append(dtos, toVolumeDTO(vol))
	}

	return &ListResponse{Volumes: dtos}, nil
}

func (h *Handler) handleInfo(ctx context.Context, params json.RawMessage) (any, error) {
	var req InfoRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" {
		return nil, fmt.Errorf("volumeId is required")
	}

	vol, err := h.svc.GetVolume(ctx, req.VolumeID)
	if err != nil {
		// Try lookup by name
		vol, err = h.svc.GetVolumeByName(ctx, req.VolumeID)
		if err != nil {
			return nil, fmt.Errorf("volume not found")
		}
	}

	return &InfoResponse{Volume: toVolumeDTO(vol)}, nil
}

func (h *Handler) handleRename(ctx context.Context, params json.RawMessage) (any, error) {
	var req RenameRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" || req.NewName == "" {
		return nil, fmt.Errorf("volumeId and newName are required")
	}

	volID := req.VolumeID
	if _, err := h.svc.GetVolume(ctx, volID); err != nil {
		// Try lookup by name
		vol, err := h.svc.GetVolumeByName(ctx, volID)
		if err == nil {
			volID = vol.ID
		}
	}

	if err := h.svc.RenameVolume(ctx, volID, req.NewName); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true}, nil
}

func (h *Handler) handleAttach(ctx context.Context, params json.RawMessage) (any, error) {
	var req AttachRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" || req.WorkspaceID == "" || req.MountPath == "" {
		return nil, fmt.Errorf("volumeId, workspaceId, and mountPath are required")
	}

	if err := h.svc.AttachVolume(ctx, req.VolumeID, req.WorkspaceID, req.MountPath); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true}, nil
}

func (h *Handler) handleDetach(ctx context.Context, params json.RawMessage) (any, error) {
	var req DetachRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" || req.WorkspaceID == "" {
		return nil, fmt.Errorf("volumeId and workspaceId are required")
	}

	if err := h.svc.DetachVolume(ctx, req.VolumeID, req.WorkspaceID); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true}, nil
}

func (h *Handler) handleDetachAll(ctx context.Context, params json.RawMessage) (any, error) {
	var req struct {
		VolumeID string `json:"volumeId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" {
		return nil, fmt.Errorf("volumeId is required")
	}

	if err := h.svc.DetachVolumeAll(ctx, req.VolumeID); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true}, nil
}

func (h *Handler) handleSnapshotCreate(ctx context.Context, params json.RawMessage) (any, error) {
	var req SnapshotCreateRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" || req.Name == "" {
		return nil, fmt.Errorf("volumeId and name are required")
	}

	snapshot, err := h.svc.CreateSnapshot(ctx, req.VolumeID, req.Name)
	if err != nil {
		return nil, err
	}

	return &SnapshotCreateResponse{Snapshot: toSnapshotDTO(snapshot)}, nil
}

func (h *Handler) handleSnapshotList(ctx context.Context, params json.RawMessage) (any, error) {
	var req SnapshotListRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" {
		return nil, fmt.Errorf("volumeId is required")
	}

	snapshots, err := h.svc.ListSnapshots(ctx, req.VolumeID)
	if err != nil {
		return nil, err
	}

	var dtos []*SnapshotDTO
	for _, snap := range snapshots {
		dtos = append(dtos, toSnapshotDTO(&snap))
	}

	return &SnapshotListResponse{Snapshots: dtos}, nil
}

func (h *Handler) handleSnapshotRestore(ctx context.Context, params json.RawMessage) (any, error) {
	var req SnapshotRestoreRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" || req.SnapshotID == "" {
		return nil, fmt.Errorf("volumeId and snapshotId are required")
	}

	if err := h.svc.RestoreSnapshot(ctx, req.VolumeID, req.SnapshotID); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true}, nil
}

func (h *Handler) handleSnapshotDelete(ctx context.Context, params json.RawMessage) (any, error) {
	var req SnapshotDeleteRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" || req.SnapshotID == "" {
		return nil, fmt.Errorf("volumeId and snapshotId are required")
	}

	if err := h.svc.DeleteSnapshot(ctx, req.VolumeID, req.SnapshotID); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true}, nil
}

func (h *Handler) handleSyncEnable(ctx context.Context, params json.RawMessage) (any, error) {
	var req SyncEnableRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" || req.LocalPath == "" {
		return nil, fmt.Errorf("volumeId and localPath are required")
	}

	direction := req.Direction
	if direction == "" {
		direction = "bidirectional"
	}

	vol, err := h.svc.EnableSync(ctx, req.VolumeID, req.LocalPath, direction)
	if err != nil {
		return nil, err
	}

	return &SyncInfoResponse{Sync: toSyncConfigDTO(vol.Sync)}, nil
}

func (h *Handler) handleSyncDisable(ctx context.Context, params json.RawMessage) (any, error) {
	var req SyncDisableRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" {
		return nil, fmt.Errorf("volumeId is required")
	}

	vol, err := h.svc.DisableSync(ctx, req.VolumeID)
	if err != nil {
		return nil, err
	}

	return &InfoResponse{Volume: toVolumeDTO(vol)}, nil
}

func (h *Handler) handleSyncInfo(ctx context.Context, params json.RawMessage) (any, error) {
	var req SyncInfoRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if req.VolumeID == "" {
		return nil, fmt.Errorf("volumeId is required")
	}

	syncConfig, err := h.svc.GetSyncConfig(ctx, req.VolumeID)
	if err != nil {
		return nil, err
	}

	return &SyncInfoResponse{Sync: toSyncConfigDTO(syncConfig)}, nil
}

func (h *Handler) handleMount(ctx context.Context, params json.RawMessage) (any, error) {
	var req MountRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.LocalPath == "" || req.WorkspaceID == "" {
		return nil, fmt.Errorf("localPath and workspaceId are required")
	}
	vol, err := h.svc.Mount(ctx, req.LocalPath, req.WorkspaceID, req.MountPath, req.Direction)
	if err != nil {
		return nil, err
	}
	return &MountResponse{Volume: toVolumeDTO(vol)}, nil
}

func (h *Handler) handleUnmount(ctx context.Context, params json.RawMessage) (any, error) {
	var req UnmountRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.VolumeID == "" || req.WorkspaceID == "" {
		return nil, fmt.Errorf("volumeId and workspaceId are required")
	}
	if err := h.svc.Unmount(ctx, req.VolumeID, req.WorkspaceID, req.Delete); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func toSyncConfigDTO(cfg *domain.SyncConfig) *SyncConfigDTO {
	if cfg == nil {
		return nil
	}
	dto := &SyncConfigDTO{
		Enabled:   cfg.Enabled,
		LocalPath: cfg.LocalPath,
		SessionID: cfg.SessionID,
		Direction: cfg.Direction,
		Status:    cfg.Status,
	}
	if cfg.LastSyncAt != nil {
		dto.LastSyncAt = cfg.LastSyncAt.Format(time.RFC3339)
	}
	return dto
}

func toSnapshotDTO(snap *domain.Snapshot) *SnapshotDTO {
	if snap == nil {
		return nil
	}
	return &SnapshotDTO{
		ID:        snap.ID,
		Name:      snap.Name,
		VolumeID:  snap.VolumeID,
		CreatedAt: snap.CreatedAt.Format(time.RFC3339),
		Size:      snap.Size,
	}
}

func toVolumeDTO(vol *domain.Volume) *VolumeDTO {
	if vol == nil {
		return nil
	}
	return &VolumeDTO{
		ID:          vol.ID,
		Name:        vol.Name,
		Type:        string(vol.Type),
		State:       string(vol.State),
		Size:        vol.Size,
		Capacity:    vol.Capacity,
		WorkspaceID: vol.WorkspaceID,
		MountPath:   vol.MountPath,
		CreatedAt:   vol.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   vol.UpdatedAt.Format(time.RFC3339),
	}
}
