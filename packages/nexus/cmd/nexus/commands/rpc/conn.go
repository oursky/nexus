package rpc

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// notification is a server-pushed JSON-RPC notification (no id field).
type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// muxMessage is the union type used when parsing incoming WebSocket frames.
type muxMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// pendingCall holds the channel a waiting Call will receive its response on.
type pendingCall struct {
	result chan muxMessage
}

// MuxConn is a multiplexed JSON-RPC 2.0 WebSocket connection.
// It routes responses (messages with an "id") to pending Call() calls, and
// routes notifications (messages with a "method" but no "id") to subscribers.
type MuxConn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]*pendingCall

	subsMu sync.Mutex
	subs   map[string][]chan json.RawMessage

	idSeq atomic.Uint64
	done  chan struct{}
	once  sync.Once
	err   error
}

// NewMuxConn wraps an existing WebSocket connection and starts the read pump.
// Close() must be called when done.
func NewMuxConn(ws *websocket.Conn) *MuxConn {
	c := &MuxConn{
		ws:      ws,
		pending: make(map[string]*pendingCall),
		subs:    make(map[string][]chan json.RawMessage),
		done:    make(chan struct{}),
	}
	go c.readPump()
	return c
}

// Close closes the underlying WebSocket connection and shuts down the read pump.
func (c *MuxConn) Close() {
	c.once.Do(func() {
		_ = c.ws.Close()
		<-c.done
	})
}

// Call sends a JSON-RPC request and waits for the matching response.
func (c *MuxConn) Call(method string, params any, out any) error {
	id := fmt.Sprintf("%d", c.idSeq.Add(1))
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	ch := make(chan muxMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = &pendingCall{result: ch}
	c.pendingMu.Unlock()

	c.writeMu.Lock()
	err := c.ws.WriteJSON(req)
	c.writeMu.Unlock()
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return fmt.Errorf("rpc send: %w", err)
	}

	select {
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		if out != nil && msg.Result != nil {
			return json.Unmarshal(msg.Result, out)
		}
		return nil
	case <-c.done:
		return fmt.Errorf("connection closed")
	case <-time.After(60 * time.Second):
		return fmt.Errorf("rpc timeout: %s", method)
	}
}

// Send sends a JSON-RPC request without waiting for a response (fire-and-forget).
func (c *MuxConn) Send(method string, params any) error {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      fmt.Sprintf("%d", c.idSeq.Add(1)),
		"method":  method,
		"params":  params,
	}
	c.writeMu.Lock()
	err := c.ws.WriteJSON(req)
	c.writeMu.Unlock()
	return err
}

// Subscribe returns a channel that receives notification params for the given
// JSON-RPC method. The caller must drain the channel. Call the returned cancel
// function to unsubscribe and close the channel.
func (c *MuxConn) Subscribe(method string) (<-chan json.RawMessage, func()) {
	ch := make(chan json.RawMessage, 128)
	c.subsMu.Lock()
	c.subs[method] = append(c.subs[method], ch)
	c.subsMu.Unlock()

	cancel := func() {
		c.subsMu.Lock()
		defer c.subsMu.Unlock()
		list := c.subs[method]
		newList := list[:0]
		for _, s := range list {
			if s != ch {
				newList = append(newList, s)
			}
		}
		c.subs[method] = newList
		close(ch)
	}
	return ch, cancel
}

// readPump continuously reads from the WebSocket and routes messages.
func (c *MuxConn) readPump() {
	defer close(c.done)
	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			c.err = err
			// Wake all pending calls.
			c.pendingMu.Lock()
			for _, p := range c.pending {
				select {
				case p.result <- muxMessage{}:
				default:
				}
			}
			c.pendingMu.Unlock()
			return
		}

		var msg muxMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		if msg.ID != "" {
			// Response to a pending call.
			c.pendingMu.Lock()
			if p, ok := c.pending[msg.ID]; ok {
				delete(c.pending, msg.ID)
				select {
				case p.result <- msg:
				default:
				}
			}
			c.pendingMu.Unlock()
		} else if msg.Method != "" {
			// Server-initiated notification.
			c.subsMu.Lock()
			subs := append([]chan json.RawMessage(nil), c.subs[msg.Method]...)
			c.subsMu.Unlock()
			for _, ch := range subs {
				select {
				case ch <- msg.Params:
				default:
					// Drop if subscriber is not keeping up.
				}
			}
		}
	}
}

// RawConn returns the underlying *websocket.Conn for use with legacy Do() calls.
// Callers must not read from the underlying connection while the MuxConn is
// also using it; this is safe only for a single-shot call before starting PTY
// subscriptions (e.g. workspace.list for name resolution).
func (c *MuxConn) RawConn() *websocket.Conn {
	return c.ws
}

// EnsureMux connects to the daemon and returns a MuxConn.
func EnsureMux() (*MuxConn, error) {
	ws, err := EnsureDaemon()
	if err != nil {
		return nil, err
	}
	return NewMuxConn(ws), nil
}
