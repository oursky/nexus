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
	appty "github.com/inizio/nexus/packages/nexus/internal/app/pty"
	domainproj "github.com/inizio/nexus/packages/nexus/internal/domain/project"
	domainws "github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	rpcerrors "github.com/inizio/nexus/packages/nexus/internal/rpc/errors"
	"github.com/inizio/nexus/packages/nexus/internal/rpc/registry"
	"github.com/inizio/nexus/packages/nexus/internal/transport"
)

// Handler provides JSON-RPC dispatch for PTY operations.
type Handler struct {
	reg    *appty.Registry
	ws     domainws.Repository
	proj   domainproj.Repository
	dialer VsockDialer
}

// VsockDialer is implemented by the Firecracker driver to open an agent
// connection and map workspace IDs to guest workdirs.
type VsockDialer interface {
	AgentConn(ctx context.Context, workspaceID string) (net.Conn, error)
	GuestWorkdir(workspaceID string) string
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

// WithVsockDialer enables remote PTY sessions for firecracker workspaces.
func WithVsockDialer(d VsockDialer) HandlerOption {
	return func(h *Handler) { h.dialer = d }
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

	if h.dialer != nil && h.ws != nil {
		ws, err := h.ws.Get(ctx, p.WorkspaceID)
		if err == nil && ws != nil && ws.Backend == "firecracker" {
			return h.createFirecrackerSession(ctx, p, ws, cols, rows)
		}
	}

	return h.createLocalSession(ctx, p, cols, rows)
}

func (h *Handler) createFirecrackerSession(ctx context.Context, p createParams, ws *domainws.Workspace, cols, rows int) (any, error) {
	conn, err := h.dialer.AgentConn(ctx, p.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("firecracker agent connect: %w", err)
	}

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

	if err := enc.Encode(map[string]any{
		"id":      sessionID,
		"type":    "shell.open",
		"command": shell,
		"workdir": workDir,
		"cols":    cols,
		"rows":    rows,
	}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("firecracker shell.open send: %w", err)
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
				ackCh <- fmt.Errorf("firecracker shell.open ack: %w", err)
				return
			}
			if env.ID != "" && env.ID != sessionID {
				continue
			}
			if env.Type == "chunk" {
				continue
			}
			if env.Type == "" {
				ackCh <- errors.New("firecracker guest agent is outdated: shell.open/shell.write protocol not supported; refresh rootfs agent payload")
				return
			}
			// Accept both "ack" (new protocol) and "result" (legacy protocol).
			if env.Type != "ack" && env.Type != "result" {
				continue
			}
			if env.ExitCode != 0 {
				ackCh <- fmt.Errorf("firecracker shell.open failed: %s", env.Stderr)
				return
			}
			ackCh <- nil
			return
		}
	}()

	select {
	case err := <-ackCh:
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
	case <-time.After(8 * time.Second):
		_ = conn.Close()
		return nil, errors.New("firecracker shell.open ack timed out")
	}

	h.reg.Register(s)
	go h.streamFirecrackerSession(s, dec, notifier)
	return s.Info(), nil
}

func (h *Handler) streamFirecrackerSession(s *appty.Session, dec *json.Decoder, notifier transport.Notifier) {
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
				notifier.Notify("pty.exit", map[string]any{"sessionId": s.ID, "exitCode": -1})
			}
			return
		}
		switch env.Type {
		case "chunk":
			notifier.Notify("pty.data", map[string]any{"sessionId": s.ID, "data": env.Data})
		case "result", "":
			if env.Stdout != "" {
				notifier.Notify("pty.data", map[string]any{"sessionId": s.ID, "data": env.Stdout})
			}
			if env.Stderr != "" {
				notifier.Notify("pty.data", map[string]any{"sessionId": s.ID, "data": env.Stderr})
			}
			notifier.Notify("pty.exit", map[string]any{"sessionId": s.ID, "exitCode": env.ExitCode})
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
				notifier.Notify("pty.data", map[string]any{
					"sessionId": s.ID,
					"data":      string(buf[:n]),
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
		data := strings.ReplaceAll(p.Data, "\r", "\n")
		if err := s.Enc.Encode(map[string]any{
			"id":   p.SessionID,
			"type": "shell.write",
			"data": data,
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
