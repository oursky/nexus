package pty

import (
	"context"
	"encoding/json"
	"fmt"

	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
)

func (h *Handler) write(_ context.Context, raw json.RawMessage) (any, error) {
	var p writeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
	}
	if p.SessionID == "" {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", "sessionId is required")
	}
	s := h.reg.Get(p.SessionID)
	if s == nil {
		return nil, ErrSessionNotFound
	}
	if s.Remote && s.Enc != nil {
		if err := s.Enc.Encode(map[string]any{
			"id":   p.SessionID,
			"type": "shell.write",
			"data": p.Data,
		}); err != nil {
			return nil, fmt.Errorf("write to remote shell: %w", err)
		}
		return map[string]any{}, nil
	}
	if s.File == nil {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", "session has no PTY file")
	}
	if _, err := s.File.WriteString(p.Data); err != nil {
		return nil, fmt.Errorf("write to PTY: %w", err)
	}
	return map[string]any{}, nil
}
