package pty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	creackpty "github.com/creack/pty"
	appty "github.com/oursky/nexus/packages/nexus/internal/app/pty"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	"github.com/oursky/nexus/packages/nexus/internal/transport"
)

func (h *Handler) create(ctx context.Context, raw json.RawMessage) (any, error) {
	var p createParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
	}
	if p.WorkspaceID == "" {
		return nil, rpcerrors.InvalidParams("pty.invalid_params", "workspaceId is required")
	}

	cols, rows := normalizeTermSize(p.Cols, p.Rows)

	ws, err := h.lookupAndCheckWorkspace(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}

	if h.shouldUseVMSession(ws) {
		return h.createVMSession(ctx, p, ws, cols, rows)
	}
	return h.createLocalSession(ctx, p, cols, rows)
}

func normalizeTermSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return cols, rows
}

func (h *Handler) lookupAndCheckWorkspace(ctx context.Context, workspaceID string) (*domainws.Workspace, error) {
	if h.ws == nil {
		return nil, nil
	}
	ws, err := h.ws.Get(ctx, workspaceID)
	if err != nil || ws == nil {
		return nil, nil
	}
	if ws.State != domainws.StateRunning {
		return nil, fmt.Errorf("workspace %s is not ready (state: %s); wait for it to finish starting", workspaceID, ws.State)
	}
	if h.ready != nil && backendUsesGuestControlChannel(ws.Backend) {
		ready, err := h.ready.WorkspaceReady(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("workspace %s readiness probe failed: %w", workspaceID, err)
		}
		if !ready {
			return nil, fmt.Errorf("workspace %s is not ready (guest provisioning still in progress); wait for workspace.ready=true", workspaceID)
		}
	}
	return ws, nil
}

func (h *Handler) shouldUseVMSession(ws *domainws.Workspace) bool {
	return h.dialer != nil && ws != nil && backendUsesGuestControlChannel(ws.Backend)
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
	if err := h.waitForShellOpenAck(sessionID, dec, conn, p.WorkspaceID, tStart); err != nil {
		return nil, err
	}

	s.SetNotifier(notifier)
	h.reg.Register(s)
	go h.streamVMSession(s, dec)
	log.Printf("pty: createVMSession: REGISTERED session=%s wsID=%s total_ms=%d", s.ID, p.WorkspaceID, time.Since(tStart).Milliseconds())
	return s.Info(), nil
}

func (h *Handler) waitForShellOpenAck(sessionID string, dec *json.Decoder, conn io.Closer, workspaceID string, tStart time.Time) error {
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
			log.Printf("pty: createVMSession: ACK ERROR wsID=%s ack_ms=%d err=%v", workspaceID, ackMs, err)
			_ = conn.Close()
			return err
		}
		log.Printf("pty: createVMSession: ACK OK wsID=%s ack_ms=%d total_ms=%d", workspaceID, ackMs, time.Since(tStart).Milliseconds())
	case <-time.After(8 * time.Second):
		log.Printf("pty: createVMSession: ACK TIMEOUT wsID=%s total_ms=%d", workspaceID, time.Since(tStart).Milliseconds())
		_ = conn.Close()
		return errors.New("guest shell.open ack timed out")
	}
	return nil
}

func backendUsesGuestControlChannel(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", "libkrun", "vm":
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
