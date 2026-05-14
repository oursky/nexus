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
//
// When pty-host is connected, most PTY operations are proxied. But VM workspaces
// (libkrun/firecracker) must bypass pty-host because pty-host creates PTYs on the
// daemon host, not inside the VM. For VM workspaces we connect directly via SSH
// to the guest agent using createVMSession / streamVMSession.

func (h *Handler) proxyCreate(ctx context.Context, raw json.RawMessage) (any, error) {
	var p createParams
	if err := json.Unmarshal(raw, &p); err != nil || p.WorkspaceID == "" {
		return h.host.Create(ctx, raw)
	}
	ws, err := h.lookupAndCheckWorkspace(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if ws != nil && h.shouldUseVMSession(ws) {
		return h.create(ctx, raw)
	}
	proxied, err := h.rewritePTYHostWorkDir(ctx, raw, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return h.host.Create(ctx, proxied)
}

// rewritePTYHostWorkDir maps logical /workspace paths to an on-disk checkout
// directory before forwarding pty.create to pty-host. pty-host runs shells on
// the engine host and cannot chdir to the guest-only /workspace path.
func (h *Handler) rewritePTYHostWorkDir(ctx context.Context, raw json.RawMessage, workspaceID string) (json.RawMessage, error) {
	if h.ws == nil {
		return raw, nil
	}
	var mp map[string]any
	if err := json.Unmarshal(raw, &mp); err != nil {
		return raw, nil
	}
	reqWd, _ := mp["workDir"].(string)
	hostDir, _, err := h.resolveHostWorkDir(ctx, workspaceID, reqWd)
	if err != nil {
		return nil, err
	}
	mp["workDir"] = hostDir
	out, err := json.Marshal(mp)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Handler) proxyList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p listParams
	if err := json.Unmarshal(raw, &p); err != nil || p.WorkspaceID == "" {
		return h.host.List(ctx, raw)
	}
	ws, err := h.lookupAndCheckWorkspace(ctx, p.WorkspaceID)
	if err == nil && ws != nil && h.shouldUseVMSession(ws) {
		return h.list(ctx, raw)
	}
	return h.host.List(ctx, raw)
}

func (h *Handler) proxyResize(ctx context.Context, raw json.RawMessage) (any, error) {
	var p resizeParams
	if err := json.Unmarshal(raw, &p); err != nil || !h.isLocalSession(p.SessionID) {
		return h.host.Resize(ctx, raw)
	}
	return h.resize(ctx, raw)
}

func (h *Handler) proxyRename(ctx context.Context, raw json.RawMessage) (any, error) {
	var p renameParams
	if err := json.Unmarshal(raw, &p); err != nil || !h.isLocalSession(p.SessionID) {
		return h.host.Rename(ctx, raw)
	}
	return h.rename(ctx, raw)
}

func (h *Handler) proxyClose(ctx context.Context, raw json.RawMessage) (any, error) {
	var p closeParams
	if err := json.Unmarshal(raw, &p); err != nil || !h.isLocalSession(p.SessionID) {
		return h.host.CloseSession(ctx, raw)
	}
	return h.close(ctx, raw)
}

func (h *Handler) proxyWrite(ctx context.Context, raw json.RawMessage) (any, error) {
	var p writeParams
	if err := json.Unmarshal(raw, &p); err != nil || !h.isLocalSession(p.SessionID) {
		return h.host.Write(ctx, raw)
	}
	return h.write(ctx, raw)
}

func (h *Handler) proxyReattach(ctx context.Context, raw json.RawMessage) (any, error) {
	var p reattachParams
	if err := json.Unmarshal(raw, &p); err != nil || !h.isLocalSession(p.SessionID) {
		return h.host.Reattach(ctx, raw)
	}
	return h.reattach(ctx, raw)
}

// isLocalSession returns true if the session lives in the daemon's local registry
// (i.e., was created via createVMSession), not in pty-host.
func (h *Handler) isLocalSession(sessionID string) bool {
	return sessionID != "" && h.reg.Get(sessionID) != nil
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
