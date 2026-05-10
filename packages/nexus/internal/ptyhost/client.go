// Package ptyhost provides a client for communicating with the PTY host process.
package ptyhost

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// Client proxies PTY RPC calls to a PTY host process via Unix socket.
type Client struct {
	socketPath string
	conn       net.Conn
	connected  atomic.Bool
	mu         sync.Mutex
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

var requestID atomic.Int32

// NewClient creates a new Client for the given socket path.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
	}
}

// Connect establishes a connection to the PTY host via Unix socket.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected.Load() {
		return nil
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial pty host: %w", err)
	}

	c.conn = conn
	c.connected.Store(true)
	return nil
}

// IsConnected returns true if the client has an active connection.
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// Close closes the client connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected.Load() {
		return nil
	}

	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.connected.Store(false)
	return nil
}

// call sends a JSON-RPC request to the PTY host and returns the result.
func (c *Client) call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if !c.connected.Load() {
		return nil, fmt.Errorf("pty host not connected")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      int(requestID.Add(1)),
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Send newline-delimited JSON-RPC
	if _, err := c.conn.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read response (newline-delimited)
	scanner := bufio.NewScanner(c.conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("pty host closed connection")
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	var result any
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return rpcResp.Result, nil
	}
	return result, nil
}

// Create proxies pty.create to the PTY host.
func (c *Client) Create(ctx context.Context, params json.RawMessage) (any, error) {
	return c.call(ctx, "pty.create", params)
}

// Write proxies pty.write to the PTY host.
func (c *Client) Write(ctx context.Context, params json.RawMessage) (any, error) {
	return c.call(ctx, "pty.write", params)
}

// Resize proxies pty.resize to the PTY host.
func (c *Client) Resize(ctx context.Context, params json.RawMessage) (any, error) {
	return c.call(ctx, "pty.resize", params)
}

// CloseSession proxies pty.close to the PTY host.
func (c *Client) CloseSession(ctx context.Context, params json.RawMessage) (any, error) {
	return c.call(ctx, "pty.close", params)
}

// Reattach proxies pty.reattach to the PTY host.
func (c *Client) Reattach(ctx context.Context, params json.RawMessage) (any, error) {
	return c.call(ctx, "pty.reattach", params)
}

// List proxies pty.list to the PTY host.
func (c *Client) List(ctx context.Context, params json.RawMessage) (any, error) {
	return c.call(ctx, "pty.list", params)
}

// Rename proxies pty.rename to the PTY host.
func (c *Client) Rename(ctx context.Context, params json.RawMessage) (any, error) {
	return c.call(ctx, "pty.rename", params)
}
