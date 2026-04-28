package pty

import (
	"context"
	"encoding/json"

	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
)

func (h *Handler) rename(_ context.Context, raw json.RawMessage) (any, error) {
	var p renameParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
	}
	if !h.reg.Rename(p.SessionID, p.Name) {
		return nil, rpcerrors.NotFound("pty.not_found", "session not found")
	}
	return map[string]bool{"ok": true}, nil
}
