package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsync "github.com/oursky/nexus/packages/nexus/internal/app/sync"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

// Handler handles sync RPC requests.
type Handler struct {
	svc SyncStarter
}

// SyncStarter defines the sync service interface.
type SyncStarter interface {
	StartSync(ctx context.Context, workspaceID, localPath string, direction string) (*appsync.SyncSessionDTO, error)
	StopSync(ctx context.Context, sessionID, workspaceID string) error
	SyncStatus(ctx context.Context, sessionID, workspaceID string) (*appsync.SyncSessionDTO, error)
	ListSyncs(ctx context.Context, workspaceID string) ([]*appsync.SyncSessionDTO, error)
}

// NewHandler creates a new sync RPC handler.
func NewHandler(svc SyncStarter) *Handler {
	return &Handler{svc: svc}
}

// Register registers sync RPC methods with the registry.
func (h *Handler) Register(reg registry.Registry) {
	reg.Register("workspace.sync-start", h.handleSyncStart)
	reg.Register("workspace.sync-stop", h.handleSyncStop)
	reg.Register("workspace.sync-status", h.handleSyncStatus)
	reg.Register("workspace.sync-list", h.handleSyncList)
}

func (h *Handler) handleSyncStart(ctx context.Context, params json.RawMessage) (any, error) {
	var req SyncStartRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.WorkspaceID == "" {
		return nil, fmt.Errorf("workspaceId is required")
	}
	if req.LocalPath == "" {
		return nil, fmt.Errorf("localPath is required")
	}
	if req.Direction == "" {
		req.Direction = "bidirectional"
	}

	session, err := h.svc.StartSync(ctx, req.WorkspaceID, req.LocalPath, req.Direction)
	if err != nil {
		return nil, err
	}

	return SyncStartResponse{
		SessionID: session.ID,
		Status:    session.Status,
	}, nil
}

func (h *Handler) handleSyncStop(ctx context.Context, params json.RawMessage) (any, error) {
	var req SyncStopRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.SessionID == "" && req.WorkspaceID == "" {
		return nil, fmt.Errorf("sessionId or workspaceId is required")
	}

	if err := h.svc.StopSync(ctx, req.SessionID, req.WorkspaceID); err != nil {
		return nil, err
	}

	return SyncStopResponse{Success: true}, nil
}

func (h *Handler) handleSyncStatus(ctx context.Context, params json.RawMessage) (any, error) {
	var req SyncStatusRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.SessionID == "" && req.WorkspaceID == "" {
		return nil, fmt.Errorf("sessionId or workspaceId is required")
	}

	session, err := h.svc.SyncStatus(ctx, req.SessionID, req.WorkspaceID)
	if err != nil {
		return nil, err
	}

	return toSyncStatusResponse(session), nil
}

func (h *Handler) handleSyncList(ctx context.Context, params json.RawMessage) (any, error) {
	var req SyncListRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	sessions, err := h.svc.ListSyncs(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}

	responses := make([]SyncStatusResponse, len(sessions))
	for i, s := range sessions {
		responses[i] = toSyncStatusResponse(s)
	}

	return SyncListResponse{Sessions: responses}, nil
}

func toSyncStatusResponse(s *appsync.SyncSessionDTO) SyncStatusResponse {
	resp := SyncStatusResponse{
		SessionID:   s.ID,
		WorkspaceID: s.WorkspaceID,
		LocalPath:   s.LocalPath,
		Status:      s.Status,
		Direction:   s.Direction,
		StartedAt:   s.StartedAt.UTC().Format(time.RFC3339),
		Stats: SyncStatsDTO{
			TotalSyncs:        s.TotalSyncs,
			BytesSent:         s.BytesSent,
			BytesReceived:     s.BytesReceived,
			FilesSent:         s.FilesSent,
			FilesReceived:     s.FilesReceived,
			ConflictsResolved: s.ConflictsResolved,
		},
	}

	if s.StoppedAt != nil && !s.StoppedAt.IsZero() {
		resp.StoppedAt = s.StoppedAt.UTC().Format(time.RFC3339)
	}
	if s.LastSyncAt != nil && !s.LastSyncAt.IsZero() {
		resp.LastSyncAt = s.LastSyncAt.UTC().Format(time.RFC3339)
	}

	return resp
}
