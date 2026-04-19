package logstream

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// LogEntry is a single structured log line.
type LogEntry struct {
	TS    time.Time      `json:"ts"`
	Level string         `json:"level"`
	Msg   string         `json:"msg"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// MarshalToJSON serialises LogEntry to JSON.
func (e LogEntry) MarshalToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// Ring is a fixed-size ring buffer of LogEntry with multi-subscriber fan-out.
// It is safe for concurrent use.
type Ring struct {
	mu    sync.Mutex
	buf   []LogEntry
	cap   int
	head  int
	count int
	subs  map[chan LogEntry]struct{}
}

// NewRing creates a Ring with the given capacity.
func NewRing(capacity int) *Ring {
	return &Ring{
		buf:  make([]LogEntry, capacity),
		cap:  capacity,
		subs: make(map[chan LogEntry]struct{}),
	}
}

// Append adds a log entry, evicting the oldest if full, and fans out to subscribers.
func (r *Ring) Append(e LogEntry) {
	r.mu.Lock()
	idx := (r.head + r.count) % r.cap
	r.buf[idx] = e
	if r.count < r.cap {
		r.count++
	} else {
		r.head = (r.head + 1) % r.cap
	}
	subs := make([]chan LogEntry, 0, len(r.subs))
	for ch := range r.subs {
		subs = append(subs, ch)
	}
	r.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Tail returns up to n most-recent entries without blocking.
func (r *Ring) Tail(n int) []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > r.count {
		n = r.count
	}
	out := make([]LogEntry, n)
	start := (r.head + r.count - n + r.cap) % r.cap
	for i := 0; i < n; i++ {
		out[i] = r.buf[(start+i)%r.cap]
	}
	return out
}

// Subscribe returns a tail of existing entries and a channel that receives new entries.
// The caller must call Unsubscribe(ch) when done.
func (r *Ring) Subscribe(tailN int) ([]LogEntry, chan LogEntry) {
	ch := make(chan LogEntry, 256)
	r.mu.Lock()
	defer r.mu.Unlock()
	n := tailN
	if n > r.count {
		n = r.count
	}
	out := make([]LogEntry, n)
	start := (r.head + r.count - n + r.cap) % r.cap
	for i := 0; i < n; i++ {
		out[i] = r.buf[(start+i)%r.cap]
	}
	r.subs[ch] = struct{}{}
	return out, ch
}

// Unsubscribe removes the subscriber and closes its channel.
func (r *Ring) Unsubscribe(ch chan LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[ch]; ok {
		delete(r.subs, ch)
		close(ch)
	}
}

// Handler implements slog.Handler, feeding all records into the Ring.
type Handler struct {
	ring  *Ring
	attrs []slog.Attr
	group string
}

// NewHandler creates a slog.Handler backed by ring.
func NewHandler(ring *Ring) *Handler {
	return &Handler{ring: ring}
}

func (h *Handler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	for _, a := range h.attrs {
		attrs[a.Key] = a.Value.Any()
	}
	h.ring.Append(LogEntry{
		TS:    r.Time,
		Level: r.Level.String(),
		Msg:   r.Message,
		Attrs: attrs,
	})
	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.attrs = append(cp.attrs, attrs...)
	return &cp
}

func (h *Handler) WithGroup(name string) slog.Handler {
	cp := *h
	cp.group = name
	return &cp
}
