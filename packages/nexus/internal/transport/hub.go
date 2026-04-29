package transport

import "sync"

// Hub is a thread-safe registry of connected Notifiers that supports
// broadcast delivery. Each WebSocket connection registers its Notifier on
// connect and unregisters on disconnect.
type Hub struct {
	mu      sync.Mutex
	clients map[uint64]Notifier
	nextID  uint64
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[uint64]Notifier)}
}

// Add registers n and returns a handle that can be passed to Remove.
func (h *Hub) Add(n Notifier) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	h.clients[id] = n
	return id
}

// Remove unregisters the Notifier associated with id.
func (h *Hub) Remove(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, id)
}

// Broadcast sends method+params to every registered Notifier. Delivery is
// best-effort; a slow or closed connection is silently skipped (Notify already
// logs errors internally).
func (h *Hub) Broadcast(method string, params any) {
	h.mu.Lock()
	clients := make([]Notifier, 0, len(h.clients))
	for _, n := range h.clients {
		clients = append(clients, n)
	}
	h.mu.Unlock()

	for _, n := range clients {
		n.Notify(method, params)
	}
}
