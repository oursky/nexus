package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	appws "github.com/inizio/nexus/packages/nexus/internal/app/workspace"
	"github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	rpce "github.com/inizio/nexus/packages/nexus/internal/rpc/errors"
)

// ── DTOs ──────────────────────────────────────────────────────────────────────

type infoReq struct {
	ID string `json:"id"`
}
type infoRes struct {
	Workspace *workspace.Workspace `json:"workspace"`
}

type createReq struct {
	Spec workspace.CreateSpec `json:"spec"`
}
type createRes struct {
	Workspace *workspace.Workspace `json:"workspace"`
}

type listReq struct{}
type listRes struct {
	Workspaces []*workspace.Workspace `json:"workspaces"`
}

type removeReq struct {
	ID string `json:"id"`
}
type removeRes struct {
	Removed bool `json:"removed"`
}

type stopReq struct {
	ID string `json:"id"`
}
type stopRes struct {
	Stopped   bool                 `json:"stopped"`
	Workspace *workspace.Workspace `json:"workspace,omitempty"`
}

type startReq struct {
	ID string `json:"id"`
}
type startRes struct {
	Workspace *workspace.Workspace `json:"workspace"`
}

type restoreReq struct {
	ID string `json:"id"`
}
type restoreRes struct {
	Restored  bool                 `json:"restored"`
	Workspace *workspace.Workspace `json:"workspace,omitempty"`
}

type forkReq struct {
	ID                 string `json:"id"`
	ChildWorkspaceName string `json:"childWorkspaceName,omitempty"`
	ChildRef           string `json:"childRef"`
}
type forkRes struct {
	Forked    bool                 `json:"forked"`
	Workspace *workspace.Workspace `json:"workspace,omitempty"`
}

type readyReq struct {
	ID string `json:"id"`
}
type readyRes struct {
	Ready bool `json:"ready"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (h *Handler) handleInfo(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[infoReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	ws, err := h.svc.Info(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &infoRes{Workspace: ws}, nil
}

func (h *Handler) handleCreate(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[createReq](raw)
	if err != nil {
		return nil, err
	}
	ws, err := h.svc.Create(ctx, req.Spec)
	if err != nil {
		return nil, mapErr(err)
	}
	return &createRes{Workspace: ws}, nil
}

func (h *Handler) handleList(ctx context.Context, raw json.RawMessage) (any, error) {
	all, err := h.svc.List(ctx)
	if err != nil {
		return nil, rpce.Internal(err.Error())
	}
	return &listRes{Workspaces: all}, nil
}

func (h *Handler) handleRemove(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[removeReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	if err := h.svc.Remove(ctx, req.ID); err != nil {
		return nil, mapErr(err)
	}
	return &removeRes{Removed: true}, nil
}

func (h *Handler) handleStop(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[stopReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	ws, err := h.svc.Stop(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &stopRes{Stopped: true, Workspace: ws}, nil
}

func (h *Handler) handleStart(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[startReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	ws, err := h.svc.Start(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &startRes{Workspace: ws}, nil
}

func (h *Handler) handleRestore(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[restoreReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	ws, err := h.svc.Restore(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &restoreRes{Restored: true, Workspace: ws}, nil
}

func (h *Handler) handleFork(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[forkReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	ws, err := h.svc.Fork(ctx, req.ID, appws.ForkSpec{
		ChildWorkspaceName: req.ChildWorkspaceName,
		ChildRef:           req.ChildRef,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &forkRes{Forked: true, Workspace: ws}, nil
}

func (h *Handler) handleReady(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[readyReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	ready, err := h.svc.Ready(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &readyRes{Ready: ready}, nil
}

func mapErr(err error) error {
	if errors.Is(err, workspace.ErrNotFound) {
		return rpce.NotFound("workspace not found")
	}
	if errors.Is(err, workspace.ErrInvalidTransition) {
		return rpce.InvalidParams(err.Error())
	}
	if rpcErr, ok := err.(*rpce.RPCError); ok {
		return rpcErr
	}
	return rpce.Internal(fmt.Sprintf("internal error: %v", err))
}

// ── Discover Ports ──────────────────────────────────────────────────────────

type discoverPortsReq struct {
	ID string `json:"id"`
}

func (h *Handler) handleDiscoverPorts(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[discoverPortsReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("id is required")
	}
	ports, err := h.svc.DiscoverPorts(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return ports, nil
}
