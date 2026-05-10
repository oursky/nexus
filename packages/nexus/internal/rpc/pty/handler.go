// Package pty provides JSON-RPC handlers for PTY session management.
package pty

import (
	"context"
	"encoding/json"

	appty "github.com/oursky/nexus/packages/nexus/internal/app/pty"
	domainproj "github.com/oursky/nexus/packages/nexus/internal/domain/project"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

// SessionRegistry tracks active PTY sessions.
type SessionRegistry interface {
	Get(id string) *appty.Session
	Register(s *appty.Session)
	Unregister(id string)
	ListByWorkspace(workspaceID string) []appty.SessionInfo
	Rename(id string, name string) bool
}

// PTYHostClient is the interface for proxying PTY operations to a PTY host.
type PTYHostClient interface {
	Create(ctx context.Context, params json.RawMessage) (any, error)
	Write(ctx context.Context, params json.RawMessage) (any, error)
	Resize(ctx context.Context, params json.RawMessage) (any, error)
	CloseSession(ctx context.Context, params json.RawMessage) (any, error)
	Reattach(ctx context.Context, params json.RawMessage) (any, error)
	List(ctx context.Context, params json.RawMessage) (any, error)
	Rename(ctx context.Context, params json.RawMessage) (any, error)
	IsConnected() bool
}

// Handler provides JSON-RPC dispatch for PTY operations.
type Handler struct {
	reg    SessionRegistry
	host   PTYHostClient
	ws     domainws.Repository
	proj   domainproj.Repository
	dialer VsockDialer
	ready  WorkspaceReadyChecker
}

// HandlerOption configures handler dependencies.
type HandlerOption func(*Handler)

// WithPTYHost wires a PTY host client for proxying PTY operations.
func WithPTYHost(host PTYHostClient) HandlerOption {
	return func(h *Handler) { h.host = host }
}

// WithWorkspaceRepo wires workspace repository lookup into PTY handler.
func WithWorkspaceRepo(ws domainws.Repository) HandlerOption {
	return func(h *Handler) { h.ws = ws }
}

// WithProjectRepo wires project repository lookup into PTY handler.
func WithProjectRepo(proj domainproj.Repository) HandlerOption {
	return func(h *Handler) { h.proj = proj }
}

// WithVsockDialer enables remote PTY sessions for VM workspaces.
func WithVsockDialer(d VsockDialer) HandlerOption {
	return func(h *Handler) { h.dialer = d }
}

// WithWorkspaceReadyChecker wires runtime readiness probing into PTY create.
func WithWorkspaceReadyChecker(c WorkspaceReadyChecker) HandlerOption {
	return func(h *Handler) { h.ready = c }
}

// New creates a new Handler backed by the given Registry.
func New(reg SessionRegistry, opts ...HandlerOption) *Handler {
	h := &Handler{reg: reg}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register wires all pty.* methods into the given RPC registry.
func (h *Handler) Register(r registry.Registry) {
	if h.host != nil && h.host.IsConnected() {
		r.Register("pty.create", h.proxyCreate)
		r.Register("pty.list", h.proxyList)
		r.Register("pty.resize", h.proxyResize)
		r.Register("pty.rename", h.proxyRename)
		r.Register("pty.close", h.proxyClose)
		r.Register("pty.write", h.proxyWrite)
		r.Register("pty.reattach", h.proxyReattach)
	} else {
		r.Register("pty.create", h.create)
		r.Register("pty.list", h.list)
		r.Register("pty.resize", h.resize)
		r.Register("pty.rename", h.rename)
		r.Register("pty.close", h.close)
		r.Register("pty.write", h.write)
		r.Register("pty.reattach", h.reattach)
	}
}

// ── Proxy methods ────────────────────────────────────────────────────────────

func (h *Handler) proxyCreate(ctx context.Context, raw json.RawMessage) (any, error) {
	return h.host.Create(ctx, raw)
}

func (h *Handler) proxyList(ctx context.Context, raw json.RawMessage) (any, error) {
	return h.host.List(ctx, raw)
}

func (h *Handler) proxyResize(ctx context.Context, raw json.RawMessage) (any, error) {
	return h.host.Resize(ctx, raw)
}

func (h *Handler) proxyRename(ctx context.Context, raw json.RawMessage) (any, error) {
	return h.host.Rename(ctx, raw)
}

func (h *Handler) proxyClose(ctx context.Context, raw json.RawMessage) (any, error) {
	return h.host.CloseSession(ctx, raw)
}

func (h *Handler) proxyWrite(ctx context.Context, raw json.RawMessage) (any, error) {
	return h.host.Write(ctx, raw)
}

func (h *Handler) proxyReattach(ctx context.Context, raw json.RawMessage) (any, error) {
	return h.host.Reattach(ctx, raw)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type createParams struct {
	WorkspaceID string   `json:"workspaceId"`
	Name        string   `json:"name"`
	Shell       string   `json:"shell"`
	Args        []string `json:"args"`
	WorkDir     string   `json:"workDir"`
	Cols        int      `json:"cols"`
	Rows        int      `json:"rows"`
}

type listParams struct {
	WorkspaceID string `json:"workspaceId"`
}

type resizeParams struct {
	SessionID string `json:"sessionId"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

type renameParams struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

type closeParams struct {
	SessionID string `json:"sessionId"`
}

type writeParams struct {
	SessionID string `json:"sessionId"`
	Data      string `json:"data"`
}

type reattachParams struct {
	SessionID string `json:"sessionId"`
}
