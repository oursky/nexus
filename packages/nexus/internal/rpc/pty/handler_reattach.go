package pty

import (
	"context"
	"encoding/json"
	"log"

	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	"github.com/oursky/nexus/packages/nexus/internal/transport"
)

// reattach redirects an existing PTY session's pty.data notifications to the
// current WebSocket connection. Called by the Mac app whenever it reconnects
// and finds live sessions from a previous connection — without this, the stream
// goroutine keeps pushing to the old (dead) notifier and the terminal is blank.
func (h *Handler) reattach(ctx context.Context, raw json.RawMessage) (any, error) {
	var p reattachParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
	}
	s := h.reg.Get(p.SessionID)
	if s == nil {
		return nil, rpcerrors.NotFound("pty.not_found", "session not found")
	}
	n := transport.NotifierFromCtx(ctx)
	s.SetNotifier(n)
	// Replay buffered output so the freshly-connected client sees existing content.
	if sb := s.Scrollback(); sb != "" {
		n.Notify("pty.data", map[string]any{"sessionId": p.SessionID, "data": sb})
	}
	log.Printf("pty: reattach: session %s reattached to new client (scrollback=%d bytes)", p.SessionID, len(s.Scrollback()))
	return map[string]bool{"ok": true}, nil
}
