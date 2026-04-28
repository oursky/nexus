package transport

import (
	"context"
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

// ── no-op notifier ────────────────────────────────────────────────────────────

type noopNotifier struct{}

func (noopNotifier) Notify(string, any) {}
