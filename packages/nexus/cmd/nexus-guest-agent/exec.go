//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func handleExec(req execRequest) execResponse {
	ctx, cancel := context.WithTimeout(context.Background(), agentExecTimeout())
	defer cancel()

	env := append([]string{}, os.Environ()...)
	if len(req.Env) > 0 {
		env = append(env, req.Env...)
	}
	env = ensurePathInEnv(env)
	env = ensureRootIdentityEnv(env)
	env = ensureSSHAuthSockEnv(env)
	env = ensureDockerHostEnv(env)

	commandPath := req.Command
	if resolved, err := lookPathInEnv(req.Command, env); err == nil {
		commandPath = resolved
	}

	cmd := exec.CommandContext(ctx, commandPath, req.Args...)
	if req.WorkDir != "" {
		if req.WorkDir == workspaceMountPoint || strings.HasPrefix(req.WorkDir, workspaceMountPoint+"/") {
			if err := setupWorkspaceMountRequiredFunc(); err != nil {
				return execResponse{ID: req.ID, ExitCode: 1, Stderr: fmt.Sprintf("workspace mount ensure failed: %v", err)}
			}
			maybeTrustMiseWorkspace(req.WorkDir, env)
		}
		cmd.Dir = req.WorkDir
	}
	cmd.Env = env

	// Capture both stdout and stderr separately
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode := 0

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = 1
			if stderrBuf.Len() == 0 {
				stderrBuf.WriteString(err.Error())
			}
		}
	}

	return execResponse{
		ID:       req.ID,
		Type:     "result",
		ExitCode: exitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
	}
}

func streamOutput(encoder *json.Encoder, id, stream string, r io.Reader) string {
	var buf strings.Builder
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text() + "\n"
		buf.WriteString(line)
		_ = encoder.Encode(execResponse{
			ID:     id,
			Type:   "chunk",
			Stream: stream,
			Data:   line,
		})
	}
	return buf.String()
}

func handleExecStreaming(req execRequest, encoder *json.Encoder) execResponse {
	ctx, cancel := context.WithTimeout(context.Background(), agentExecTimeout())
	defer cancel()

	env := append([]string{}, os.Environ()...)
	if len(req.Env) > 0 {
		env = append(env, req.Env...)
	}
	env = ensurePathInEnv(env)
	env = ensureRootIdentityEnv(env)
	env = ensureSSHAuthSockEnv(env)
	env = ensureDockerHostEnv(env)

	commandPath := req.Command
	if resolved, err := lookPathInEnv(req.Command, env); err == nil {
		commandPath = resolved
	}

	cmd := exec.CommandContext(ctx, commandPath, req.Args...)
	if req.WorkDir != "" {
		if req.WorkDir == workspaceMountPoint || strings.HasPrefix(req.WorkDir, workspaceMountPoint+"/") {
			if err := setupWorkspaceMountRequiredFunc(); err != nil {
				return execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: fmt.Sprintf("workspace mount ensure failed: %v", err)}
			}
			maybeTrustMiseWorkspace(req.WorkDir, env)
		}
		cmd.Dir = req.WorkDir
	}
	cmd.Env = env

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: err.Error()}
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: err.Error()}
	}

	if err := cmd.Start(); err != nil {
		return execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: err.Error()}
	}

	stdoutCh := make(chan string, 1)
	stderrCh := make(chan string, 1)
	go func() { stdoutCh <- streamOutput(encoder, req.ID, "stdout", stdoutPipe) }()
	go func() { stderrCh <- streamOutput(encoder, req.ID, "stderr", stderrPipe) }()

	err = cmd.Wait()
	stdout := <-stdoutCh
	stderr := <-stderrCh

	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = 1
			if stderr == "" {
				stderr = err.Error()
			}
		}
	}

	return execResponse{ID: req.ID, Type: "result", ExitCode: exitCode, Stdout: stdout, Stderr: stderr}
}

func agentExecTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("AGENT_EXEC_TIMEOUT_SEC"))
	if raw == "" {
		return 10 * time.Minute
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 10 * time.Minute
	}

	return time.Duration(seconds) * time.Second
}

func ensurePathInEnv(env []string) []string {
	defaultPath := "/root/.local/share/mise/shims:/root/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	for i, entry := range env {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		pathValue := strings.TrimSpace(strings.TrimPrefix(entry, "PATH="))
		if pathValue == "" {
			env[i] = "PATH=" + defaultPath
			return env
		}
		if !strings.Contains(pathValue, "/root/.local/share/mise/shims") {
			env[i] = "PATH=/root/.local/share/mise/shims:/root/.local/bin:" + pathValue
		}
		return env
	}

	return append(env, "PATH="+defaultPath)
}

func ensureSSHAuthSockEnv(env []string) []string {
	return ensureEnvVar(env, "SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")
}

func ensureDockerHostEnv(env []string) []string {
	if !isTSINetworkMode() {
		return env
	}
	const dockerSocket = "/var/lib/docker/docker.sock"
	if _, err := os.Stat(dockerSocket); err != nil {
		return env
	}
	return ensureEnvVar(env, "DOCKER_HOST", "unix://"+dockerSocket)
}

func ensureRootIdentityEnv(env []string) []string {
	env = ensureEnvVar(env, "HOME", "/root")
	env = ensureEnvVar(env, "USER", "root")
	env = ensureEnvVar(env, "LOGNAME", "root")
	env = ensureEnvVar(env, "SHELL", "/bin/sh")
	// Default TERM to xterm-256color so TUI apps (opencode, etc.) render
	// correctly.  The value is overridden by whatever the connecting client
	// sends in the PTY env, so interactive sessions from the Mac app get the
	// right TERM automatically.
	env = ensureEnvVar(env, "TERM", "xterm-256color")
	env = ensureEnvVar(env, "COLORTERM", "truecolor")
	return env
}

func ensureEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	desired := prefix + value
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = desired
			return env
		}
	}
	return append(env, desired)
}

func lookPathInEnv(command string, env []string) (string, error) {
	if strings.Contains(command, "/") {
		return command, nil
	}

	pathValue := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
			break
		}
	}

	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, command)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}

	return "", exec.ErrNotFound
}

func maybeTrustMiseWorkspace(workDir string, env []string) {
	misePath, err := lookPathInEnv("mise", env)
	if err != nil {
		return
	}
	// Non-interactive shells skip untrusted configs; trust project configs up-front.
	cmd := exec.Command(misePath, "trust", "-a")
	cmd.Dir = workDir
	cmd.Env = env
	_ = cmd.Run()
}
