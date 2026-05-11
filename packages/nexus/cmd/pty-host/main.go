// pty-host is a standalone long-lived process that owns all PTY sessions.
// It survives daemon restarts and exposes PTY operations over a Unix socket.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	creackpty "github.com/creack/pty"
	appty "github.com/oursky/nexus/packages/nexus/internal/app/pty"
	rpcerrors "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	rpcregistry "github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
	"github.com/oursky/nexus/packages/nexus/internal/transport"
)

var ErrSessionNotFound = fmt.Errorf("session not found")

func main() {
	socketPath := flag.String("socket", "", "Unix socket path to listen on")
	flag.Parse()

	if *socketPath == "" {
		log.Fatal("pty-host: --socket is required")
	}

	reg := appty.NewRegistry()
	rpcReg := rpcregistry.NewMapRegistry()

	// Register all PTY methods
	rpcReg.Register("pty.create", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			WorkspaceID string   `json:"workspaceId"`
			Name        string   `json:"name"`
			Shell       string   `json:"shell"`
			Args        []string `json:"args"`
			WorkDir     string   `json:"workDir"`
			Cols        int      `json:"cols"`
			Rows        int      `json:"rows"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
		}
		if p.WorkspaceID == "" {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", "workspaceId is required")
		}

		cols, rows := p.Cols, p.Rows
		if cols <= 0 {
			cols = 80
		}
		if rows <= 0 {
			rows = 24
		}

		shell := resolveShell(p.Shell)
		args := p.Args
		if len(args) == 0 {
			args = []string{"-l"}
		}

		cmd := exec.CommandContext(context.WithoutCancel(ctx), shell, args...)
		if p.WorkDir != "" {
			cmd.Dir = p.WorkDir
		}

		s := &appty.Session{
			ID:          fmt.Sprintf("pty-%d", timeNowNano()),
			WorkspaceID: p.WorkspaceID,
			Name:        p.Name,
			Shell:       shell,
			WorkDir:     p.WorkDir,
			Cols:        cols,
			Rows:        rows,
			Cmd:         cmd,
			Done:        make(chan struct{}),
			CreatedAt:   timeNow(),
		}

		ptmx, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{
			Cols: uint16(cols),
			Rows: uint16(rows),
		})
		if err != nil {
			return nil, fmt.Errorf("pty start: %w", err)
		}
		s.File = ptmx

		reg.Register(s)
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
				reg.Unregister(s.ID)
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
	})

	rpcReg.Register("pty.write", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"sessionId"`
			Data      string `json:"data"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
		}
		if p.SessionID == "" {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", "sessionId is required")
		}
		s := reg.Get(p.SessionID)
		if s == nil {
			return nil, ErrSessionNotFound
		}
		if s.File == nil {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", "session has no PTY file")
		}
		if _, err := s.File.WriteString(p.Data); err != nil {
			return nil, fmt.Errorf("write to PTY: %w", err)
		}
		return map[string]any{}, nil
	})

	rpcReg.Register("pty.resize", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"sessionId"`
			Cols      int    `json:"cols"`
			Rows      int    `json:"rows"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
		}
		s := reg.Get(p.SessionID)
		if s == nil {
			return nil, rpcerrors.NotFound("pty.not_found", "session not found")
		}
		s.Mu.Lock()
		s.Cols = p.Cols
		s.Rows = p.Rows
		f := s.File
		s.Mu.Unlock()

		if f != nil {
			_ = creackpty.Setsize(f, &creackpty.Winsize{
				Cols: uint16(p.Cols),
				Rows: uint16(p.Rows),
			})
		}
		return map[string]bool{"ok": true}, nil
	})

	rpcReg.Register("pty.close", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
		}
		s := reg.Get(p.SessionID)
		if s == nil {
			return nil, rpcerrors.NotFound("pty.not_found", "session not found")
		}
		s.Closing.Store(true)
		if s.File != nil {
			_ = s.File.Close()
		}
		if s.Cmd != nil && s.Cmd.Process != nil {
			_ = s.Cmd.Process.Kill()
		}
		reg.Unregister(p.SessionID)
		return map[string]bool{"ok": true}, nil
	})

	rpcReg.Register("pty.reattach", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
		}
		s := reg.Get(p.SessionID)
		if s == nil {
			return nil, ErrSessionNotFound
		}
		n := transport.NotifierFromCtx(ctx)
		s.SetNotifier(n)
		if sb := s.Scrollback(); sb != "" {
			n.Notify("pty.data", map[string]any{"sessionId": p.SessionID, "data": sb})
		}
		log.Printf("pty: reattach: session %s reattached (scrollback=%d bytes)", p.SessionID, len(s.Scrollback()))
		return map[string]bool{"ok": true}, nil
	})

	rpcReg.Register("pty.list", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			WorkspaceID string `json:"workspaceId"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
		}
		if p.WorkspaceID == "" {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", "workspaceId is required")
		}
		sessions := reg.ListByWorkspace(p.WorkspaceID)
		if sessions == nil {
			sessions = []appty.SessionInfo{}
		}
		return map[string]any{"sessions": sessions}, nil
	})

	rpcReg.Register("pty.rename", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"sessionId"`
			Name      string `json:"name"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, rpcerrors.InvalidParams("pty.invalid_params", err.Error())
		}
		if !reg.Rename(p.SessionID, p.Name) {
			return nil, rpcerrors.NotFound("pty.not_found", "session not found")
		}
		return map[string]bool{"ok": true}, nil
	})

	lst := transport.NewListener(*socketPath, rpcReg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		log.Println("pty-host: shutting down...")
		cancel()
	}()

	log.Printf("pty-host: listening on %s", *socketPath)
	if err := lst.Serve(ctx); err != nil && err != context.Canceled {
		log.Fatalf("pty-host: serve error: %v", err)
	}
	log.Println("pty-host: stopped")
}

func resolveShell(requested string) string {
	if requested != "" {
		if resolved, err := exec.LookPath(requested); err == nil {
			return resolved
		}
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		if resolved, err := exec.LookPath(sh); err == nil {
			return resolved
		}
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	return "/bin/sh"
}

func timeNow() time.Time { return time.Now() }
func timeNowNano() int64 { return time.Now().UnixNano() }
