// Package pty provides JSON-RPC handlers for PTY session management.
package pty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	creackpty "github.com/creack/pty"
	appty "github.com/oursky/nexus/packages/nexus/internal/app/pty"
	domainproj "github.com/oursky/nexus/packages/nexus/internal/domain/project"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
	"github.com/oursky/nexus/packages/nexus/internal/transport"
)

// Handler provides JSON-RPC dispatch for PTY operations.
type Handler struct {
	reg    *appty.Registry
	ws     domainws.Repository
	proj   domainproj.Repository
	dialer VsockDialer
	ready  WorkspaceReadyChecker
}

// VsockDialer is implemented by VM runtime drivers to open an agent
// connection and map workspace IDs to guest workdirs.
type VsockDialer interface {
	AgentConn(ctx context.Context, workspaceID string) (net.Conn, error)
	GuestWorkdir(workspaceID string) string
}

// WorkspaceReadyChecker lets the PTY handler gate terminal creation on
// runtime-specific readiness checks (toolchain/bootstrap completed).
type WorkspaceReadyChecker interface {
	WorkspaceReady(ctx context.Context, workspaceID string) (bool, error)
}

// HandlerOption configures handler dependencies.
type HandlerOption func(*Handler)

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
func New(reg *appty.Registry, opts ...HandlerOption) *Handler {
	h := &Handler{reg: reg}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register wires all pty.* methods into the given RPC registry.
func (h *Handler) Register(r registry.Registry) {
	r.Register("pty.create", h.create)
	r.Register("pty.list", h.list)
	r.Register("pty.resize", h.resize)
	r.Register("pty.rename", h.rename)
	r.Register("pty.close", h.close)
	r.Register("pty.write", h.write)
	r.Register("pty.reattach", h.reattach)
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

// ── Handlers ──────────────────────────────────────────────────────────────────

func (h *Handler) create(ctx context.Context, raw json.RawMessage) (any, error) {
	var p createParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams(err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("workspaceId is required")
	}

	cols, rows := p.Cols, p.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	// Look up workspace once; used for both the state guard and session routing.
	var ws *domainws.Workspace
	if h.ws != nil {
		if found, err := h.ws.Get(ctx, p.WorkspaceID); err == nil && found != nil {
			ws = found
		}
	}

	// Guard: reject PTY connections until the workspace is fully running.
	// The workspace transitions to StateRunning only after driver.Start()
	// completes (including any guest toolchain bootstrap).  Enforcing this here
	// prevents the UI from opening a terminal into a partially-started workspace.
	if ws != nil && ws.State != domainws.StateRunning {
		return nil, fmt.Errorf("workspace %s is not ready (state: %s); wait for it to finish starting", p.WorkspaceID, ws.State)
	}
	if h.ready != nil && ws != nil && backendUsesGuestControlChannel(ws.Backend) {
		ready, err := h.ready.WorkspaceReady(ctx, p.WorkspaceID)
		if err != nil {
			return nil, fmt.Errorf("workspace %s readiness probe failed: %w", p.WorkspaceID, err)
		}
		if !ready {
			return nil, fmt.Errorf("workspace %s is not ready (guest provisioning still in progress); wait for workspace.ready=true", p.WorkspaceID)
		}
	}

	if h.dialer != nil && ws != nil && backendUsesGuestControlChannel(ws.Backend) {
		return h.createVMSession(ctx, p, ws, cols, rows)
	}

	return h.createLocalSession(ctx, p, cols, rows)
}

func (h *Handler) createVMSession(ctx context.Context, p createParams, ws *domainws.Workspace, cols, rows int) (any, error) {
	tStart := time.Now()
	log.Printf("pty: createVMSession: START wsID=%s", p.WorkspaceID)
	conn, err := h.dialer.AgentConn(ctx, p.WorkspaceID)
	agentMs := time.Since(tStart).Milliseconds()
	if err != nil {
		log.Printf("pty: createVMSession: AgentConn FAIL wsID=%s agent_ms=%d err=%v", p.WorkspaceID, agentMs, err)
		return nil, fmt.Errorf("guest agent connect: %w", err)
	}
	log.Printf("pty: createVMSession: AgentConn OK wsID=%s agent_ms=%d", p.WorkspaceID, agentMs)

	workDir := h.dialer.GuestWorkdir(p.WorkspaceID)
	if workDir == "" {
		workDir = "/workspace"
	}
	if stringsTrimmed := p.WorkDir; stringsTrimmed != "" {
		workDir = stringsTrimmed
	}

	shell := p.Shell
	if shell == "" {
		shell = "bash"
	}

	sessionID := fmt.Sprintf("pty-%d", time.Now().UnixNano())
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	notifier := transport.NotifierFromCtx(ctx)

	s := &appty.Session{
		ID:          sessionID,
		WorkspaceID: p.WorkspaceID,
		Name:        p.Name,
		Shell:       shell,
		WorkDir:     workDir,
		Cols:        cols,
		Rows:        rows,
		RemoteConn:  conn,
		Enc:         enc,
		Dec:         dec,
		Remote:      true,
		Done:        make(chan struct{}),
		CreatedAt:   time.Now(),
	}

	shellOpenMsg := map[string]any{
		"id":      sessionID,
		"type":    "shell.open",
		"command": shell,
		"args":    p.Args,
		"workdir": workDir,
		"cols":    cols,
		"rows":    rows,
	}
	if err := enc.Encode(shellOpenMsg); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("guest shell.open send: %w", err)
	}

	_ = ws
	ackCh := make(chan error, 1)
	go func() {
		for {
			var env struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				ExitCode int    `json:"exit_code"`
				Stderr   string `json:"stderr"`
			}
			if err := dec.Decode(&env); err != nil {
				ackCh <- fmt.Errorf("guest shell.open ack: %w", err)
				return
			}
			if env.ID != "" && env.ID != sessionID {
				continue
			}
			if env.Type == "chunk" {
				continue
			}
			if env.Type == "" {
				ackCh <- errors.New("guest agent is outdated: shell.open/shell.write protocol not supported; refresh rootfs agent payload")
				return
			}
			// Accept both "ack" (new protocol) and "result" (legacy protocol).
			if env.Type != "ack" && env.Type != "result" {
				continue
			}
			if env.ExitCode != 0 {
				ackCh <- fmt.Errorf("guest shell.open failed: %s", env.Stderr)
				return
			}
			ackCh <- nil
			return
		}
	}()

	tAck := time.Now()
	select {
	case err := <-ackCh:
		ackMs := time.Since(tAck).Milliseconds()
		if err != nil {
			log.Printf("pty: createVMSession: ACK ERROR wsID=%s ack_ms=%d err=%v", p.WorkspaceID, ackMs, err)
			_ = conn.Close()
			return nil, err
		}
		log.Printf("pty: createVMSession: ACK OK wsID=%s ack_ms=%d total_ms=%d", p.WorkspaceID, ackMs, time.Since(tStart).Milliseconds())
	case <-time.After(8 * time.Second):
		log.Printf("pty: createVMSession: ACK TIMEOUT wsID=%s total_ms=%d", p.WorkspaceID, time.Since(tStart).Milliseconds())
		_ = conn.Close()
		return nil, errors.New("guest shell.open ack timed out")
	}

	s.SetNotifier(notifier)
	h.reg.Register(s)
	go h.streamVMSession(s, dec)
	log.Printf("pty: createVMSession: REGISTERED session=%s wsID=%s total_ms=%d", s.ID, p.WorkspaceID, time.Since(tStart).Milliseconds())
	return s.Info(), nil
}

func backendUsesGuestControlChannel(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", "firecracker", "libkrun":
		return true
	default:
		return false
	}
}

func (h *Handler) streamVMSession(s *appty.Session, dec *json.Decoder) {
	log.Printf("pty: streamVMSession: starting for %s", s.ID)
	defer func() {
		_ = s.RemoteConn.Close()
		h.reg.Unregister(s.ID)
		if !s.Closing.Load() {
			close(s.Done)
		}
	}()

	for {
		var env struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Data     string `json:"data"`
			ExitCode int    `json:"exit_code"`
			Stderr   string `json:"stderr"`
			Stdout   string `json:"stdout"`
		}
		if err := dec.Decode(&env); err != nil {
			if !s.Closing.Load() {
				log.Printf("pty: vsock read error for session %s: %v", s.ID, err)
				// Read the current notifier so pty.exit reaches whoever is attached now.
				s.GetNotifier().Notify("pty.exit", map[string]any{"sessionId": s.ID, "exitCode": -1})
			}
			return
		}
		log.Printf("pty: streamVMSession: received type=%q id=%q", env.Type, env.ID)
		n := s.GetNotifier()
		switch env.Type {
		case "chunk":
			s.AppendScrollback(env.Data)
			n.Notify("pty.data", map[string]any{"sessionId": s.ID, "data": env.Data})
		case "result", "":
			log.Printf("pty: streamVMSession: sending pty.exit exitCode=%d", env.ExitCode)
			if env.Stdout != "" {
				n.Notify("pty.data", map[string]any{"sessionId": s.ID, "data": env.Stdout})
			}
			if env.Stderr != "" {
				n.Notify("pty.data", map[string]any{"sessionId": s.ID, "data": env.Stderr})
			}
			n.Notify("pty.exit", map[string]any{"sessionId": s.ID, "exitCode": env.ExitCode})
			return
		case "ack":
			// shell.write ack
		}
	}
}

func (h *Handler) createLocalSession(ctx context.Context, p createParams, cols, rows int) (any, error) {
	hostWorkDir := p.WorkDir
	logicalWorkDir := p.WorkDir
	if h.ws != nil {
		var err error
		hostWorkDir, logicalWorkDir, err = h.resolveHostWorkDir(ctx, p.WorkspaceID, p.WorkDir)
		if err != nil {
			return nil, err
		}
	}

	shell := resolveShell(p.Shell)
	args := p.Args
	if len(args) == 0 {
		args = []string{"-l"}
	}

	cmd := exec.CommandContext(context.WithoutCancel(ctx), shell, args...) //nolint:gosec
	if hostWorkDir != "" {
		cmd.Dir = hostWorkDir
	}

	s := &appty.Session{
		ID:          fmt.Sprintf("pty-%d", time.Now().UnixNano()),
		WorkspaceID: p.WorkspaceID,
		Name:        p.Name,
		Shell:       shell,
		WorkDir:     logicalWorkDir,
		Cols:        cols,
		Rows:        rows,
		Cmd:         cmd,
		Done:        make(chan struct{}),
		CreatedAt:   time.Now(),
	}

	ptmx, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}
	s.File = ptmx

	h.reg.Register(s)
	notifier := transport.NotifierFromCtx(ctx)

	go func() {
		defer func() {
			exitCode := 0
			if waitErr := cmd.Wait(); waitErr != nil {
				if exitErr, ok := waitErr.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				}
			}
			_ = ptmx.Close()
			h.reg.Unregister(s.ID)
			close(s.Done)
			notifier.Notify("pty.exit", map[string]any{"sessionId": s.ID, "exitCode": exitCode})
		}()

		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				s.AppendScrollback(chunk)
				notifier.Notify("pty.data", map[string]any{
					"sessionId": s.ID,
					"data":      chunk,
				})
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("pty: read error for session %s: %v", s.ID, err)
				}
				return
			}
		}
	}()

	return s.Info(), nil
}

func (h *Handler) list(_ context.Context, raw json.RawMessage) (any, error) {
	var p listParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams(err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("workspaceId is required")
	}
	sessions := h.reg.ListByWorkspace(p.WorkspaceID)
	if sessions == nil {
		sessions = []appty.SessionInfo{}
	}
	return map[string]any{"sessions": sessions}, nil
}

func (h *Handler) resize(_ context.Context, raw json.RawMessage) (any, error) {
	var p resizeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams(err.Error())
	}
	s := h.reg.Get(p.SessionID)
	if s == nil {
		return nil, rpcerrors.NotFound("session not found")
	}
	s.Mu.Lock()
	s.Cols = p.Cols
	s.Rows = p.Rows
	f := s.File
	enc := s.Enc
	remote := s.Remote
	s.Mu.Unlock()

	if remote && enc != nil {
		_ = enc.Encode(map[string]any{
			"id":   p.SessionID,
			"type": "shell.resize",
			"cols": p.Cols,
			"rows": p.Rows,
		})
	} else if f != nil {
		_ = creackpty.Setsize(f, &creackpty.Winsize{
			Cols: uint16(p.Cols),
			Rows: uint16(p.Rows),
		})
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) rename(_ context.Context, raw json.RawMessage) (any, error) {
	var p renameParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams(err.Error())
	}
	if !h.reg.Rename(p.SessionID, p.Name) {
		return nil, rpcerrors.NotFound("session not found")
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) close(_ context.Context, raw json.RawMessage) (any, error) {
	var p closeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams(err.Error())
	}
	s := h.reg.Get(p.SessionID)
	if s == nil {
		return nil, rpcerrors.NotFound("session not found")
	}
	s.Closing.Store(true)
	if s.Remote && s.Enc != nil {
		_ = s.Enc.Encode(map[string]any{
			"id":   p.SessionID,
			"type": "shell.close",
		})
	}
	if s.RemoteConn != nil {
		_ = s.RemoteConn.Close()
	}
	if s.File != nil {
		_ = s.File.Close()
	}
	if s.Cmd != nil && s.Cmd.Process != nil {
		_ = s.Cmd.Process.Kill()
	}
	h.reg.Unregister(p.SessionID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) write(_ context.Context, raw json.RawMessage) (any, error) {
	var p writeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams(err.Error())
	}
	if p.SessionID == "" {
		return nil, rpcerrors.InvalidParams("sessionId is required")
	}
	s := h.reg.Get(p.SessionID)
	if s == nil {
		return nil, rpcerrors.NotFound("session not found")
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
		return nil, rpcerrors.InvalidParams("session has no PTY file")
	}
	if _, err := s.File.WriteString(p.Data); err != nil {
		return nil, fmt.Errorf("write to PTY: %w", err)
	}
	return map[string]any{}, nil
}

// reattach redirects an existing PTY session's pty.data notifications to the
// current WebSocket connection. Called by the Mac app whenever it reconnects
// and finds live sessions from a previous connection — without this, the stream
// goroutine keeps pushing to the old (dead) notifier and the terminal is blank.
func (h *Handler) reattach(ctx context.Context, raw json.RawMessage) (any, error) {
	var p reattachParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams(err.Error())
	}
	s := h.reg.Get(p.SessionID)
	if s == nil {
		return nil, rpcerrors.NotFound("session not found")
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

func defaultShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		if resolved, err := exec.LookPath(sh); err == nil {
			return resolved
		}
		if filepath.IsAbs(sh) {
			if fi, err := os.Stat(sh); err == nil && !fi.IsDir() {
				return sh
			}
		}
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	return "/bin/sh"
}

func resolveShell(requested string) string {
	requested = filepath.Clean(requested)
	if requested != "" {
		if resolved, err := exec.LookPath(requested); err == nil {
			return resolved
		}
		if filepath.IsAbs(requested) {
			if _, err := os.Stat(requested); err == nil {
				return requested
			}
		}
	}
	return defaultShell()
}
