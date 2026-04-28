package pty

import (
	"context"
	"encoding/json"

	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
)

func (h *Handler) close(_ context.Context, raw json.RawMessage) (any, error) {
	var p closeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
	}
	s := h.reg.Get(p.SessionID)
	if s == nil {
		return nil, rpcerrors.NotFound("pty.not_found", "session not found")
	}
	s.Closing.Store(true)
	if s.Remote && s.Enc != nil {
		_ = s.Enc.Encode(map[string]any{
			"id":   p.SessionID,
			"type": "shell.close",
		})
	}
	if s.RemoteConn != nil {
		_ = s.RemoteConn.Close()
	}
	if s.File != nil {
		_ = s.File.Close()
	}
	if s.Cmd != nil && s.Cmd.Process != nil {
		_ = s.Cmd.Process.Kill()
	}
	h.reg.Unregister(p.SessionID)
	return map[string]bool{"ok": true}, nil
}
