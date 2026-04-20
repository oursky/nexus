package transport

import (
	"context"
	"encoding/json"
	"log"
)

// Notifier pushes server-initiated JSON-RPC notifications to a connected client.
type Notifier interface {
	Notify(method string, params any)
}

type contextKey struct{}

// WithNotifier returns a child context carrying n.
func WithNotifier(ctx context.Context, n Notifier) context.Context {
	return context.WithValue(ctx, contextKey{}, n)
}

// NotifierFromCtx extracts the Notifier from ctx, or returns a no-op if absent.
func NotifierFromCtx(ctx context.Context) Notifier {
	if n, ok := ctx.Value(contextKey{}).(Notifier); ok && n != nil {
		return n
	}
	return noopNotifier{}
}

// ── channel-backed notifier ───────────────────────────────────────────────────

type chanNotifier struct {
	ch chan<- []byte
}

func (n chanNotifier) Notify(method string, params any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		log.Printf("transport: notifier marshal: %v", err)
		return
	}
	// Non-blocking: drop if the channel is full or closed.
	select {
	case n.ch <- b:
	default:
		log.Printf("transport: notifier: channel full, dropping %s", method)
	}
}

// ── no-op notifier ────────────────────────────────────────────────────────────

type noopNotifier struct{}

func (noopNotifier) Notify(string, any) {}
