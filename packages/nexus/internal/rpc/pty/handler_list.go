package pty

import (
	"context"
	"encoding/json"

	appty "github.com/oursky/nexus/packages/nexus/internal/app/pty"
	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
)

func (h *Handler) list(_ context.Context, raw json.RawMessage) (any, error) {
	var p listParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", "workspaceId is required")
	}
	sessions := h.reg.ListByWorkspace(p.WorkspaceID)
	if sessions == nil {
		sessions = []appty.SessionInfo{}
	}
	return map[string]any{"sessions": sessions}, nil
}
