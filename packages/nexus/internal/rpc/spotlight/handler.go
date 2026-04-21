package spotlight

import (
	"context"
	"encoding/json"

	appspotlight "github.com/oursky/nexus/packages/nexus/internal/app/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

// Handler handles spotlight RPC methods.
type Handler struct {
	svc *appspotlight.Service
}

// New creates a Handler wrapping the given service.
func New(svc *appspotlight.Service) *Handler {
	return &Handler{svc: svc}
}

// Register registers all handled methods on the registry.
func (h *Handler) Register(reg registry.Registry) error {
	reg.Register("spotlight.start", h.HandleStart)
	reg.Register("spotlight.list", h.HandleList)
	reg.Register("spotlight.stop", h.HandleStop)
	reg.Register("workspace.ports.list", h.HandlePortsList)
	reg.Register("workspace.ports.add", h.HandlePortsAdd)
	reg.Register("workspace.ports.remove", h.HandlePortsRemove)
	return nil
}

// --- spotlight.start ---

type startParams struct {
	WorkspaceID string               `json:"workspaceId"`
	Spec        spotlight.ExposeSpec `json:"spec"`
}

type startResult struct {
	Forward *spotlight.Forward `json:"forward"`
}

func (h *Handler) HandleStart(ctx context.Context, raw json.RawMessage) (any, error) {
	var p startParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("invalid params: " + err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("workspaceId is required")
	}
	p.Spec.WorkspaceID = p.WorkspaceID

	fwd, err := h.svc.StartSpotlight(ctx, p.WorkspaceID, p.Spec)
	if err != nil {
		return nil, rpcerrors.Internal(err.Error())
	}
	return &startResult{Forward: fwd}, nil
}

// --- spotlight.list ---

type listParams struct {
	WorkspaceID string `json:"workspaceId"`
}

type listResult struct {
	Forwards []*spotlight.Forward `json:"forwards"`
}

func (h *Handler) HandleList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p listParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("invalid params: " + err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("workspaceId is required")
	}

	fwds, err := h.svc.ListForwards(ctx, p.WorkspaceID)
	if err != nil {
		return nil, rpcerrors.Internal(err.Error())
	}
	if fwds == nil {
		fwds = []*spotlight.Forward{}
	}
	return &listResult{Forwards: fwds}, nil
}

// --- spotlight.stop ---

type stopParams struct {
	WorkspaceID string `json:"workspaceId"`
}

type closeResult struct {
	Closed bool `json:"closed"`
}

func (h *Handler) HandleStop(ctx context.Context, raw json.RawMessage) (any, error) {
	var p stopParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("invalid params: " + err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("workspaceId is required")
	}

	if err := h.svc.StopWorkspaceSpotlight(ctx, p.WorkspaceID); err != nil {
		return nil, rpcerrors.Internal(err.Error())
	}
	return &closeResult{Closed: true}, nil
}

// --- workspace.ports.list ---

type portsListParams struct {
	WorkspaceID string `json:"workspaceId"`
}

func (h *Handler) HandlePortsList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p portsListParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("invalid params: " + err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("workspaceId is required")
	}

	fwds, err := h.svc.ListForwards(ctx, p.WorkspaceID)
	if err != nil {
		return nil, rpcerrors.Internal(err.Error())
	}
	if fwds == nil {
		fwds = []*spotlight.Forward{}
	}
	return &listResult{Forwards: fwds}, nil
}

// --- workspace.ports.add ---

type portsAddParams struct {
	WorkspaceID string               `json:"workspaceId"`
	Spec        spotlight.ExposeSpec `json:"spec"`
}

func (h *Handler) HandlePortsAdd(ctx context.Context, raw json.RawMessage) (any, error) {
	var p portsAddParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("invalid params: " + err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("workspaceId is required")
	}
	p.Spec.WorkspaceID = p.WorkspaceID

	fwd, err := h.svc.StartSpotlight(ctx, p.WorkspaceID, p.Spec)
	if err != nil {
		return nil, rpcerrors.Internal(err.Error())
	}
	return &startResult{Forward: fwd}, nil
}

// --- workspace.ports.remove ---

type portsRemoveParams struct {
	WorkspaceID string `json:"workspaceId"`
	ForwardID   string `json:"forwardId"`
}

func (h *Handler) HandlePortsRemove(ctx context.Context, raw json.RawMessage) (any, error) {
	var p portsRemoveParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("invalid params: " + err.Error())
	}
	if p.ForwardID == "" {
		return nil, rpcerrors.InvalidParams("forwardId is required")
	}

	if err := h.svc.CloseForward(ctx, p.ForwardID); err != nil {
		return nil, rpcerrors.Internal(err.Error())
	}
	return &closeResult{Closed: true}, nil
}

