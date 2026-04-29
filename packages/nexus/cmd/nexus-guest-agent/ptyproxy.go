//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	creackpty "github.com/creack/pty"
)

type shellSession struct {
	id   string
	cmd  *exec.Cmd
	ptmx *os.File
	done chan int
}

var (
	shellSessions   = map[string]*shellSession{}
	shellSessionsMu sync.Mutex
)

func handleShellOpen(req execRequest, encoder *json.Encoder) {
	env := append([]string{}, os.Environ()...)
	env = ensurePathInEnv(env)
	env = ensureRootIdentityEnv(env)
	env = ensureSSHAuthSockEnv(env)
	env = ensureDockerHostEnv(env)

	workDir := workspaceMountPoint
	if strings.TrimSpace(req.WorkDir) != "" {
		workDir = req.WorkDir
	}
	if workDir == workspaceMountPoint || strings.HasPrefix(workDir, workspaceMountPoint+"/") {
		if err := setupWorkspaceMountRequiredFunc(); err != nil {
			_ = encoder.Encode(execResponse{ID: req.ID, Type: "ack", ExitCode: 1, Stderr: fmt.Sprintf("workspace mount ensure failed: %v", err)})
			return
		}
	}

	shell := req.Command
	if strings.TrimSpace(shell) == "" {
		shell = "bash"
	}

	// If args are provided, honour them (e.g. ["-c", "ip route show"] from
	// `nexus workspace exec`). Otherwise fall back to a login shell.
	shellArgs := req.Args
	if len(shellArgs) == 0 {
		shellArgs = []string{"-l"}
	}

	// Interactive shell sessions are long-lived; do not tie them to a short-lived
	// request context that gets canceled when this handler returns.
	cmd := exec.Command(shell, shellArgs...)
	cmd.Dir = workDir
	cmd.Env = env

	cols := req.Cols
	rows := req.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	ptmx, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "ack", ExitCode: 1, Stderr: err.Error()})
		return
	}
	s := &shellSession{id: req.ID, cmd: cmd, ptmx: ptmx, done: make(chan int, 1)}

	shellSessionsMu.Lock()
	shellSessions[req.ID] = s
	shellSessionsMu.Unlock()

	_ = encoder.Encode(execResponse{ID: req.ID, Type: "ack", ExitCode: 0})

	var readWg sync.WaitGroup
	readWg.Add(1)
	go func() {
		defer readWg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_ = encoder.Encode(execResponse{ID: req.ID, Type: "chunk", Stream: "stdout", Data: string(buf[:n])})
			}
			if err != nil {
				return
			}
			if n == 0 {
				// PTY slave closed (EOF on master).
				return
			}
		}
	}()

	go func() {
		err := cmd.Wait()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		// Wait for the read goroutine to finish draining all output
		// from the PTY master. The read goroutine exits when the slave
		// is closed (shell exits) or when EOF is reached.
		readWg.Wait()
		_ = ptmx.Close()
		s.done <- exitCode
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "result", ExitCode: exitCode})
		shellSessionsMu.Lock()
		delete(shellSessions, req.ID)
		shellSessionsMu.Unlock()
	}()
}

func handleShellWrite(req execRequest, encoder *json.Encoder) {
	shellSessionsMu.Lock()
	s, ok := shellSessions[req.ID]
	shellSessionsMu.Unlock()
	if !ok {
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: "shell session not found"})
		return
	}

	if _, err := io.WriteString(s.ptmx, req.Data); err != nil {
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: err.Error()})
		return
	}

	_ = encoder.Encode(execResponse{ID: req.ID, Type: "ack", ExitCode: 0})
}

func handleShellResize(req execRequest, encoder *json.Encoder) {
	shellSessionsMu.Lock()
	s, ok := shellSessions[req.ID]
	shellSessionsMu.Unlock()
	if !ok {
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: "shell session not found"})
		return
	}
	cols := req.Cols
	rows := req.Rows
	if cols <= 0 || rows <= 0 {
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: "invalid resize dimensions"})
		return
	}
	if err := creackpty.Setsize(s.ptmx, &creackpty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: err.Error()})
		return
	}
	_ = encoder.Encode(execResponse{ID: req.ID, Type: "ack", ExitCode: 0})
}

func handleShellClose(req execRequest, encoder *json.Encoder) {
	shellSessionsMu.Lock()
	s, ok := shellSessions[req.ID]
	if ok {
		delete(shellSessions, req.ID)
	}
	shellSessionsMu.Unlock()

	if !ok {
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "ack", ExitCode: 0})
		return
	}

	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
	_ = encoder.Encode(execResponse{ID: req.ID, Type: "ack", ExitCode: 0})
}
