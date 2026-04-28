package pty

import (
	"context"
	"encoding/json"

	creackpty "github.com/creack/pty"
	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
)

func (h *Handler) resize(_ context.Context, raw json.RawMessage) (any, error) {
	var p resizeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
	}
	s := h.reg.Get(p.SessionID)
	if s == nil {
		return nil, rpcerrors.NotFound("pty.not_found", "session not found")
	}
	s.Mu.Lock()
	s.Cols = p.Cols
	s.Rows = p.Rows
	f := s.File
	enc := s.Enc
	remote := s.Remote
	s.Mu.Unlock()

	if remote && enc != nil {
		_ = enc.Encode(map[string]any{
			"id":   p.SessionID,
			"type": "shell.resize",
			"cols": p.Cols,
			"rows": p.Rows,
		})
	} else if f != nil {
		_ = creackpty.Setsize(f, &creackpty.Winsize{
			Cols: uint16(p.Cols),
			Rows: uint16(p.Rows),
		})
	}
	return map[string]bool{"ok": true}, nil
}
