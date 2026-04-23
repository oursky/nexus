//go:build linux

package main

import (
	"bufio"
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
	"strconv"
	"strings"
	"sync"
	"time"

	creackpty "github.com/creack/pty"
	"github.com/mdlayher/vsock"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/firecracker"
	"golang.org/x/sys/unix"
)

const (
	defaultAgentPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

var (
	setupWorkspaceMountFunc         = setupWorkspaceMount
	setupWorkspaceMountRequiredFunc = setupWorkspaceMountRequired
	workspaceMountPoint             = "/workspace"
	workspaceDevicePath             = "/dev/vdb"
	workspaceDeviceAttempts         = 300
	workspaceDeviceInterval         = 100 * time.Millisecond
	workspaceMkdirAll               = os.MkdirAll
	workspaceStat                   = os.Stat
	workspaceMountFunc              = unix.Mount
	workspaceUnmountFunc            = unix.Unmount
	workspaceReadProcMounts         = os.ReadFile
	kernelMkdirAll                  = os.MkdirAll
	kernelMountFunc                 = unix.Mount
)

// Request types
type execRequest struct {
	ID      string   `json:"id"`
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	WorkDir string   `json:"workdir,omitempty"`
	Env     []string `json:"env,omitempty"`
	Stream  bool     `json:"stream,omitempty"`
	Data    string   `json:"data,omitempty"`
	Cols    int      `json:"cols,omitempty"`
	Rows    int      `json:"rows,omitempty"`
}

type execResponse struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Data     string `json:"data,omitempty"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

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
	for i, entry := range env {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(entry, "PATH=")) == "" {
			env[i] = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		}
		return env
	}

	return append(env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
}

func ensureSSHAuthSockEnv(env []string) []string {
	return ensureEnvVar(env, "SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")
}

func ensureRootIdentityEnv(env []string) []string {
	env = ensureEnvVar(env, "HOME", "/root")
	env = ensureEnvVar(env, "USER", "root")
	env = ensureEnvVar(env, "LOGNAME", "root")
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

func serveConn(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		// Parse request
		var req execRequest
		if err := decoder.Decode(&req); err != nil {
			if err != io.EOF {
				log.Printf("Error decoding request: %v", err)
				// Try to send error response with request ID if available
				encoder.Encode(execResponse{ID: req.ID, ExitCode: 1, Stderr: fmt.Sprintf("decode error: %v", err)})
			}
			return
		}

		// Validate request ID is present
		if strings.TrimSpace(req.ID) == "" {
			log.Printf("Request missing ID field")
			encoder.Encode(execResponse{ExitCode: 1, Stderr: "request ID is required"})
			continue
		}

		if strings.TrimSpace(req.Type) != "" {
			handleShellRequest(req, encoder)
			continue
		}

		// Handle request
		resp := execResponse{}
		if req.Stream {
			resp = handleExecStreaming(req, encoder)
		} else {
			resp = handleExec(req)
		}

		// Send response
		if err := encoder.Encode(resp); err != nil {
			log.Printf("Error encoding response: %v", err)
			return
		}
	}
}

func handleShellRequest(req execRequest, encoder *json.Encoder) {
	switch req.Type {
	case "shell.open":
		handleShellOpen(req, encoder)
	case "shell.write":
		handleShellWrite(req, encoder)
	case "shell.resize":
		handleShellResize(req, encoder)
	case "shell.close":
		handleShellClose(req, encoder)
	default:
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: "unknown shell request type"})
	}
}

func handleShellOpen(req execRequest, encoder *json.Encoder) {
	env := append([]string{}, os.Environ()...)
	env = ensurePathInEnv(env)
	env = ensureRootIdentityEnv(env)
	env = ensureSSHAuthSockEnv(env)

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

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_ = encoder.Encode(execResponse{ID: req.ID, Type: "chunk", Stream: "stdout", Data: string(buf[:n])})
			}
			if err != nil {
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
		s.done <- exitCode
		_ = ptmx.Close()
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

func main() {
	emitDiagnostic("agent boot pid=%d", os.Getpid())

	bootstrapGuestEnvironment(os.Getpid())
	startSSHAgentProxy()
	go startSpotlightListener()

	listener, transport, err := resolveListener()
	if err != nil {
		emitDiagnostic("agent listener setup failed: %v", err)
		log.Fatalf("Failed to listen: %v", err)
	}
	defer listener.Close()

	emitDiagnostic("agent listener ready transport=%s", transport)
	log.Printf("Firecracker agent listening (%s)", transport)

	for {
		conn, err := listener.Accept()
		if err != nil {
			emitDiagnostic("agent accept failed: %v", err)
			log.Printf("Failed to accept connection: %v", err)
			continue
		}
		emitDiagnostic("agent accepted connection")
		go serveConn(conn)
	}
}

// startSpotlightListener starts the spotlight port-forward vsock listener.
// Each accepted connection reads "FORWARD <port>\n" then proxies raw TCP to
// 127.0.0.1:<port> inside the guest. This lets the daemon host reach services
// that only listen on loopback without relying on bridge networking.
func startSpotlightListener() {
	port := firecracker.DefaultSpotlightVSockPort

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		emitDiagnostic("spotlight listener: socket: %v", err)
		return
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		_ = unix.Close(fd)
		emitDiagnostic("spotlight listener: bind vsock port %d: %v", port, err)
		return
	}
	if err := unix.Listen(fd, 64); err != nil {
		_ = unix.Close(fd)
		emitDiagnostic("spotlight listener: listen: %v", err)
		return
	}
	file := os.NewFile(uintptr(fd), "spotlight-vsock-listener")
	defer file.Close()

	listener, err := vsock.FileListener(file)
	if err != nil {
		_ = unix.Close(fd)
		emitDiagnostic("spotlight listener: FileListener: %v", err)
		return
	}
	defer listener.Close()

	emitDiagnostic("spotlight listener: ready on vsock port %d", port)
	for {
		conn, err := listener.Accept()
		if err != nil {
			emitDiagnostic("spotlight listener: accept: %v", err)
			return
		}
		go serveSpotlightForward(conn)
	}
}

// serveSpotlightForward reads "FORWARD <port>\n" and proxies to 127.0.0.1:<port>.
func serveSpotlightForward(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "FORWARD ") {
		_, _ = conn.Write([]byte("ERR invalid command\n"))
		return
	}
	portStr := strings.TrimPrefix(line, "FORWARD ")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		_, _ = conn.Write([]byte("ERR invalid port\n"))
		return
	}

	target, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		_, _ = conn.Write([]byte(fmt.Sprintf("ERR %v\n", err)))
		return
	}
	defer target.Close()

	if _, err := conn.Write([]byte("OK\n")); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, reader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, target); done <- struct{}{} }()
	<-done
}

// mountKernelFilesystemsFunc is overridable in tests.
var mountKernelFilesystemsFunc = mountKernelFilesystems

// setupDNSFunc is overridable in tests.
var setupDNSFunc = setupDNS

func bootstrapGuestEnvironment(pid int) {
	ensureAgentProcessPath()

	if pid == 1 {
		mountKernelFilesystemsFunc()
		if err := setupPTYDevices(); err != nil {
			emitDiagnostic("agent pty device setup failed (non-fatal): %v", err)
		}
		emitDiagnostic("agent pid1 kernel filesystems mounted")
	}

	if overlayMode() {
		// Overlay mode: /dev/vdb = shared base (ro), /dev/vdc = writable overlay.
		// Set up overlayfs at /workspace combining both.
		if err := setupWorkspaceOverlay(); err != nil {
			emitDiagnostic("agent workspace overlay mount failed (non-fatal): %v", err)
		} else {
			emitDiagnostic("agent workspace overlay mounted")
			// Replace the global workspace-mount helpers so that on-demand
			// mount checks in handleExec (WorkDir=/workspace) verify the overlay
			// rather than trying to re-mount /dev/vdb directly.
			setupWorkspaceMountFunc = ensureWorkspaceOverlayMounted
			setupWorkspaceMountRequiredFunc = ensureWorkspaceOverlayMounted
		}
	} else {
		if err := setupWorkspaceMountFunc(); err != nil {
			emitDiagnostic("agent workspace mount failed (non-fatal): %v", err)
		} else {
			emitDiagnostic("agent workspace mounted")
		}
	}

	// Mount the host config drive and apply configs.
	if err := applyHostConfigDrive(); err != nil {
		emitDiagnostic("agent host config drive (non-fatal): %v", err)
	} else {
		emitDiagnostic("agent host config applied")
	}

	if err := setupNetwork(); err != nil {
		emitDiagnostic("agent network setup failed (non-fatal): %v", err)
	} else {
		emitDiagnostic("agent network configured")
	}

	if pid == 1 {
		maybeStartSSHDInGuest()
	}

	setupDNSFunc()
	emitDiagnostic("agent dns configured")

	// Install CLI tools in the background so boot/PTY readiness is not blocked
	// on npm network operations (which can also fill the rootfs if run synchronously).
	go ensureGuestCLITools()

	if pid == 1 {
		if err := ensureDockerDaemon(); err != nil {
			emitDiagnostic("agent docker daemon setup failed (non-fatal): %v", err)
		}
	}
}

const hostConfigMount = "/run/nexus-host"

// overlayMode reports whether the kernel was booted with nexus.overlay=1,
// which means /dev/vdb is a read-only base image and /dev/vdc is the writable
// overlay. The host config drive is at /dev/vdd in this mode.
func overlayMode() bool {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == "nexus.overlay=1" {
			return true
		}
	}
	return false
}

// hostConfigDevicePath returns the block device that carries the host config
// drive. In overlay mode it shifts to /dev/vdd (vdc is the overlay drive).
func hostConfigDevicePath() string {
	if overlayMode() {
		return "/dev/vdd"
	}
	return "/dev/vdc"
}

// applyHostConfigDrive mounts the host config ext4 image at /run/nexus-host
// and copies known config files into place.
func applyHostConfigDrive() error {
	hostConfigDevice := hostConfigDevicePath()
	if _, err := os.Stat(hostConfigDevice); err != nil {
		return nil // drive not attached — nothing to do
	}

	if err := os.MkdirAll(hostConfigMount, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", hostConfigMount, err)
	}

	if err := unix.Mount(hostConfigDevice, hostConfigMount, "ext4", unix.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("mount %s: %w", hostConfigDevice, err)
	}

	type copySpec struct {
		src, dst string
		mode     os.FileMode
	}
	copies := []copySpec{
		{".gitconfig", "/root/.gitconfig", 0o644},
		{".ssh/known_hosts", "/root/.ssh/known_hosts", 0o644},
		{".ssh/config", "/root/.ssh/config", 0o600},
		{".ssh/authorized_keys", "/root/.ssh/authorized_keys", 0o600},
		{".resolv.conf", "/etc/resolv.conf", 0o644},
		{".config/gh/hosts.yml", "/root/.config/gh/hosts.yml", 0o600},
		{".config/gh/config.yml", "/root/.config/gh/config.yml", 0o600},
		{".config/opencode/opencode.json", "/root/.config/opencode/opencode.json", 0o644},
		{".config/opencode/ocx.jsonc", "/root/.config/opencode/ocx.jsonc", 0o644},
		{".opencode/opencode.json", "/root/.opencode/opencode.json", 0o644},
		{".opencode/opencode.jsonc", "/root/.opencode/opencode.jsonc", 0o644},
		{".config/claude/credentials.json", "/root/.config/claude/credentials.json", 0o600},
		{".config/claude/settings.json", "/root/.config/claude/settings.json", 0o644},
	}

	_ = os.MkdirAll("/root/.ssh", 0o700)

	for _, c := range copies {
		src := filepath.Join(hostConfigMount, c.src)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // file not in image — skip
		}
		if err := os.MkdirAll(filepath.Dir(c.dst), 0o755); err != nil {
			continue
		}
		_ = os.WriteFile(c.dst, data, c.mode)
	}

	// Ensure git safe.directory for the workspace.
	_ = exec.Command("git", "config", "--global", "--add", "safe.directory", "/workspace").Run()
	_ = exec.Command("git", "config", "--global", "--add", "safe.directory", "*").Run()
	// Strip platform-specific credential helpers that won't work in the VM.
	_ = exec.Command("git", "config", "--global", "--unset-all", "credential.helper").Run()

	// Write SSH_AUTH_SOCK to profile so all shells pick it up.
	profileLine := "\nexport SSH_AUTH_SOCK=/tmp/ssh-agent.sock\n"
	f, err := os.OpenFile("/root/.profile", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(profileLine)
		f.Close()
	}

	return nil
}

func ensureGuestCLITools() {
	type cliSpec struct {
		binary string
		pkg    string
	}

	tools := []cliSpec{
		{binary: "opencode", pkg: "opencode-ai@latest"},
		{binary: "codex", pkg: "@openai/codex@latest"},
		{binary: "claude", pkg: "@anthropic-ai/claude-code@latest"},
	}

	npmPath, err := lookPathInEnv("npm", os.Environ())
	hasNPM := err == nil && strings.TrimSpace(npmPath) != ""

	missing := make([]cliSpec, 0, len(tools))
	for _, tool := range tools {
		if err := writeCLIWrapper(filepath.Join("/usr/local/bin", tool.binary), tool.pkg); err != nil {
			emitDiagnostic("agent cli wrapper write failed for %s: %v", tool.binary, err)
		}
		if guestCLIAvailable(tool.binary) {
			continue
		}
		missing = append(missing, tool)
	}
	if len(missing) == 0 {
		return
	}

	if !hasNPM {
		names := make([]string, 0, len(missing))
		for _, tool := range missing {
			names = append(names, tool.binary)
		}
		emitDiagnostic("agent cli tools: npm not found, wrappers only (missing=%s)", strings.Join(names, ", "))
		return
	}

	// Redirect npm cache off the rootfs to avoid filling it.
	// In overlay mode use the raw ext4 store directly so npm lock/cache
	// operations happen on a native filesystem rather than through overlayfs.
	npmCacheDir := workspaceMountPoint + "/.npm-cache"
	if overlayMode() {
		npmCacheDir = "/mnt/nexus-overlay/.npm-cache"
	}
	_ = os.MkdirAll(npmCacheDir, 0o755)

	var packages []string
	for _, tool := range missing {
		packages = append(packages, tool.pkg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	args := append([]string{"install", "-g", "--force", "--cache", npmCacheDir}, packages...)
	out, err := exec.CommandContext(ctx, npmPath, args...).CombinedOutput()
	if err != nil {
		emitDiagnostic("agent cli tools install failed: %v: %s", err, strings.TrimSpace(string(out)))
		return
	}

	stillMissing := make([]string, 0, len(missing))
	for _, tool := range missing {
		if !guestCLIAvailable(tool.binary) {
			stillMissing = append(stillMissing, tool.binary)
		}
	}
	if len(stillMissing) > 0 {
		emitDiagnostic("agent cli tools install incomplete (missing=%s)", strings.Join(stillMissing, ", "))
		return
	}

	emitDiagnostic("agent cli tools installed and verified: %s", strings.Join(packages, ", "))
}

func writeCLIWrapper(destPath, pkg string) error {
	content := fmt.Sprintf(`#!/bin/sh
exec npx -y %s "$@"
`, pkg)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".nexus-wrapper-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destPath)
}

func guestCLIAvailable(binary string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Env = ensurePathInEnv(os.Environ())
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func ensureAgentProcessPath() {
	if strings.TrimSpace(os.Getenv("PATH")) == "" {
		_ = os.Setenv("PATH", defaultAgentPath)
	}
}

// setupWorkspaceOverlay sets up an overlayfs at /workspace using:
//   - /dev/vdb as the read-only lower layer (shared base image with repo files)
//   - /dev/vdc as the read-write upper+work store (per-workspace writable overlay)
//
// This is used when the kernel was booted with nexus.overlay=1.
func setupWorkspaceOverlay() error {
	const (
		baseDevice   = "/dev/vdb"
		overlayDev   = "/dev/vdc"
		baseMnt      = "/mnt/nexus-base"
		overlayStore = "/mnt/nexus-overlay"
	)

	// Wait for both block devices to appear.
	for _, dev := range []string{baseDevice, overlayDev} {
		for i := 0; i < 300; i++ {
			if _, err := os.Stat(dev); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if _, err := os.Stat(dev); err != nil {
			return fmt.Errorf("device %s not available after 30s", dev)
		}
	}

	// Mount base read-only.
	if err := os.MkdirAll(baseMnt, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", baseMnt, err)
	}
	if err := unix.Mount(baseDevice, baseMnt, "ext4", unix.MS_RDONLY, ""); err != nil && !errors.Is(err, unix.EBUSY) {
		return fmt.Errorf("mount base %s: %w", baseDevice, err)
	}

	// Mount overlay store read-write.
	if err := os.MkdirAll(overlayStore, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", overlayStore, err)
	}
	if err := unix.Mount(overlayDev, overlayStore, "ext4", 0, ""); err != nil && !errors.Is(err, unix.EBUSY) {
		return fmt.Errorf("mount overlay store %s: %w", overlayDev, err)
	}

	// Create upper and work directories inside the overlay store.
	upperDir := overlayStore + "/upper"
	workDir := overlayStore + "/work"
	if err := os.MkdirAll(upperDir, 0o755); err != nil {
		return fmt.Errorf("mkdir upper: %w", err)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("mkdir work: %w", err)
	}

	// Set up overlayfs at /workspace.
	if err := os.MkdirAll(workspaceMountPoint, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", workspaceMountPoint, err)
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", baseMnt, upperDir, workDir)
	if err := unix.Mount("overlay", workspaceMountPoint, "overlay", 0, opts); err != nil && !errors.Is(err, unix.EBUSY) {
		return fmt.Errorf("mount overlayfs at %s: %w", workspaceMountPoint, err)
	}

	return nil
}

// ensureWorkspaceOverlayMounted verifies that /workspace is already mounted as
// an overlayfs. If not, it attempts to mount it. Used in overlay mode as the
// replacement for setupWorkspaceMountRequiredFunc so that WorkDir=/workspace
// requests in handleExec don't try to re-mount /dev/vdb directly.
func ensureWorkspaceOverlayMounted() error {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return fmt.Errorf("read /proc/mounts: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == workspaceMountPoint && fields[2] == "overlay" {
			return nil // already mounted
		}
	}
	// Not yet mounted — run the full overlay setup.
	return setupWorkspaceOverlay()
}

func setupWorkspaceMount() error {
	return setupWorkspaceMountWithRequirement(false)
}

func setupWorkspaceMountRequired() error {
	return setupWorkspaceMountWithRequirement(true)
}

func setupWorkspaceMountWithRequirement(required bool) error {
	if err := workspaceMkdirAll(workspaceMountPoint, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", workspaceMountPoint, err)
	}

	available, err := waitForWorkspaceDevice()
	if err != nil {
		return err
	}
	if !available {
		if required {
			return fmt.Errorf("workspace device %s not available", workspaceDevicePath)
		}
		return nil
	}

	if err := workspaceMountFunc(workspaceDevicePath, workspaceMountPoint, "ext4", 0, ""); err != nil {
		if errors.Is(err, unix.EBUSY) {
			mounted, mErr := workspaceMountIsActive(workspaceDevicePath)
			if mErr != nil {
				return fmt.Errorf("verify workspace mount after EBUSY: %w", mErr)
			}
			if mounted {
				return nil
			}
			if err := workspaceUnmountNonWorkspaceMounts(); err != nil {
				return fmt.Errorf("clear conflicting workspace mounts after EBUSY: %w", err)
			}
			if retryErr := workspaceMountFunc(workspaceDevicePath, workspaceMountPoint, "ext4", 0, ""); retryErr == nil {
				return nil
			} else if !errors.Is(retryErr, unix.EBUSY) {
				return fmt.Errorf("retry mount %s at %s after clearing conflicts: %w", workspaceDevicePath, workspaceMountPoint, retryErr)
			}
			mounted, mErr = workspaceMountIsActive(workspaceDevicePath)
			if mErr != nil {
				return fmt.Errorf("verify workspace mount after retry EBUSY: %w", mErr)
			}
			if mounted {
				return nil
			}
			return fmt.Errorf("mount %s at %s returned EBUSY but workspace mount is not active", workspaceDevicePath, workspaceMountPoint)
		}
		return fmt.Errorf("mount %s at %s: %w", workspaceDevicePath, workspaceMountPoint, err)
	}

	return nil
}

func workspaceMountIsActive(devicePath string) (bool, error) {
	raw, err := workspaceReadProcMounts("/proc/mounts")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == workspaceMountPoint {
			return fields[0] == devicePath, nil
		}
	}
	return false, nil
}

func workspaceUnmountNonWorkspaceMounts() error {
	raw, err := workspaceReadProcMounts("/proc/mounts")
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mountPoint := fields[1]
		if mountPoint == workspaceMountPoint || !strings.HasPrefix(mountPoint, workspaceMountPoint+"/") {
			continue
		}
		if err := workspaceUnmountFunc(mountPoint, 0); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}

func waitForWorkspaceDevice() (bool, error) {
	attempts := workspaceDeviceAttempts
	if attempts <= 0 {
		attempts = 1
	}

	interval := workspaceDeviceInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if _, err := workspaceStat(workspaceDevicePath); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("stat %s: %w", workspaceDevicePath, err)
		}

		if attempt < attempts-1 {
			time.Sleep(interval)
		}
	}

	return false, nil
}

func mountKernelFilesystems() {
	_ = kernelMkdirAll("/proc", 0o755)
	_ = kernelMkdirAll("/sys", 0o755)
	_ = kernelMkdirAll("/dev", 0o755)
	_ = kernelMountFunc("proc", "/proc", "proc", 0, "")
	_ = kernelMountFunc("sysfs", "/sys", "sysfs", 0, "")
	_ = kernelMountFunc("devtmpfs", "/dev", "devtmpfs", 0, "")
	mountCgroupFilesystems()
}

func setupPTYDevices() error {
	if err := os.MkdirAll("/dev/pts", 0o755); err != nil {
		return fmt.Errorf("mkdir /dev/pts: %w", err)
	}
	if err := kernelMountFunc("devpts", "/dev/pts", "devpts", 0, "ptmxmode=0666,mode=0620"); err != nil && !errors.Is(err, unix.EBUSY) {
		return fmt.Errorf("mount devpts: %w", err)
	}
	if _, err := os.Lstat("/dev/ptmx"); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat /dev/ptmx: %w", err)
		}
		if linkErr := os.Symlink("pts/ptmx", "/dev/ptmx"); linkErr != nil && !os.IsExist(linkErr) {
			return fmt.Errorf("symlink /dev/ptmx -> pts/ptmx: %w", linkErr)
		}
	}
	return nil
}

func ensureDockerDaemon() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	if _, err := exec.LookPath("dockerd"); err != nil {
		return nil
	}
	if err := exec.Command("docker", "info").Run(); err == nil {
		emitDiagnostic("agent docker daemon already available")
		return nil
	}

	// In overlay mode /workspace is an overlayfs mount. Docker's overlay2
	// storage driver cannot create overlayfs mounts inside an overlayfs
	// directory. Store Docker data directly on the underlying ext4 so that
	// overlay2 works. The data persists across VM restarts because it lives
	// inside workspace-overlay.ext4.
	dataRoot := "/workspace/.nexus-docker"
	execRoot := "/workspace/.nexus-docker-exec"
	if overlayMode() {
		dataRoot = "/mnt/nexus-overlay/.nexus-docker"
		execRoot = "/mnt/nexus-overlay/.nexus-docker-exec"
	}

	_ = os.Remove("/var/run/docker.pid")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir docker data root: %w", err)
	}
	if err := os.MkdirAll(execRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir docker exec root: %w", err)
	}

	iptablesFlag := "--iptables=false"
	if _, err := exec.LookPath("iptables-legacy"); err == nil {
		iptablesFlag = "--iptables=true"
	} else if _, err := exec.LookPath("iptables"); err == nil {
		iptablesFlag = "--iptables=true"
	}

	logFile, err := os.OpenFile("/tmp/nexus-agent-dockerd.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open dockerd log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(
		"dockerd",
		"--host=unix:///var/run/docker.sock",
		"--data-root="+dataRoot,
		"--exec-root="+execRoot,
		"--storage-driver=overlay2",
		iptablesFlag,
	)
	cmd.Env = ensurePathInEnv(os.Environ())
	disableRaw := strings.TrimSpace(os.Getenv("NEXUS_DOCKER_DISABLE_IPTABLES_RAW"))
	if disableRaw == "" || disableRaw == "1" {
		cmd.Env = append(cmd.Env, "DOCKER_INSECURE_NO_IPTABLES_RAW=1")
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dockerd: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := exec.Command("docker", "info").Run(); err == nil {
			emitDiagnostic("agent docker daemon ready")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("dockerd did not become ready within 15s (see /tmp/nexus-agent-dockerd.log)")
}

func mountCgroupFilesystems() {
	_ = kernelMkdirAll("/sys/fs/cgroup", 0o755)
	if err := kernelMountFunc("none", "/sys/fs/cgroup", "cgroup2", 0, ""); err == nil || errors.Is(err, unix.EBUSY) {
		return
	}

	if err := kernelMountFunc("tmpfs", "/sys/fs/cgroup", "tmpfs", 0, "mode=755"); err != nil && !errors.Is(err, unix.EBUSY) {
		return
	}

	controllers := []string{"cpuset", "cpu", "cpuacct", "blkio", "memory", "devices", "freezer", "net_cls", "perf_event", "net_prio", "hugetlb", "pids"}
	for _, controller := range controllers {
		path := "/sys/fs/cgroup/" + controller
		_ = kernelMkdirAll(path, 0o755)
		err := kernelMountFunc("cgroup", path, "cgroup", 0, controller)
		if err != nil && !errors.Is(err, unix.EBUSY) {
			continue
		}
	}
}

// setupNetworkFunc is the function used to configure the guest network interface.
// Overridable in tests.
var setupNetworkFunc = realSetupNetwork

// setupNetwork brings up eth0 via DHCP using udhcpc.
// It is called at PID1 boot before setupDNS so that DNS resolution works.
func setupNetwork() error {
	return setupNetworkFunc()
}

// maybeStartSSHDInGuest launches OpenSSH sshd when the rootfs provides it.
// The rootfs is built by firecracker-setup.sh which installs openssh-server,
// so sshd should always be present. Host keys are generated on first boot.
// This enables VS Code / Cursor Remote-SSH to attach directly to the micro-VM.
func maybeStartSSHDInGuest() {
	const sshdPath = "/usr/sbin/sshd"
	if _, err := os.Stat(sshdPath); err != nil {
		emitDiagnostic("sshd: %s not found in rootfs — rebuild rootfs with 'nexus daemon start' to install openssh-server", sshdPath)
		return
	}

	// Generate any missing host keys so sshd starts cleanly.
	for _, keyType := range []string{"rsa", "ecdsa", "ed25519"} {
		keyPath := "/etc/ssh/ssh_host_" + keyType + "_key"
		if _, err := os.Stat(keyPath); err != nil {
			out, kErr := exec.Command("ssh-keygen", "-q", "-N", "", "-t", keyType, "-f", keyPath).CombinedOutput()
			if kErr != nil {
				emitDiagnostic("sshd: generate %s host key failed (non-fatal): %v: %s", keyType, kErr, strings.TrimSpace(string(out)))
			} else {
				emitDiagnostic("sshd: generated %s host key", keyType)
			}
		}
	}

	// /run/sshd must be a directory (privilege separation chroot) — not a file.
	// Remove any stale file that may exist (e.g. from a previous bad attempt)
	// and re-create as a directory.
	const sshdRunDir = "/run/sshd"
	if info, err := os.Lstat(sshdRunDir); err == nil && !info.IsDir() {
		_ = os.Remove(sshdRunDir)
	}
	_ = os.MkdirAll(sshdRunDir, 0o755)

	cmd := exec.Command(sshdPath)
	if err := cmd.Start(); err != nil {
		emitDiagnostic("sshd: start failed (non-fatal): %v", err)
		return
	}
	emitDiagnostic("sshd: started (pid=%d)", cmd.Process.Pid)
}

func realSetupNetwork() error {
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set lo up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Bring the interface up first so udhcpc can configure it.
	if out, err := exec.Command("ip", "link", "set", "eth0", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set eth0 up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if _, err := exec.LookPath("udhcpc"); err == nil {
		// Run udhcpc in one-shot mode: -n (exit on failure), -q (quit after obtaining
		// lease), -t 10 (retry 10 times), -T 3 (3s between retries) -> max ~30s wait.
		out, err := exec.Command(
			"udhcpc", "-i", "eth0", "-n", "-q", "-t", "10", "-T", "3",
		).CombinedOutput()
		if err == nil {
			return nil
		}
		emitDiagnostic("udhcpc failed, falling back to static guest IP: %v: %s", err, strings.TrimSpace(string(out)))
	}

	if err := configureStaticGuestNetwork(); err != nil {
		return fmt.Errorf("static network fallback failed: %w", err)
	}
	return nil
}

// readNexusGateway returns the bridge gateway IP from the host config drive
// (.nexus-gateway), falling back to the compiled-in default.
func readNexusGateway() string {
	const configDriveMount = "/run/nexus-host"
	const defaultGW = "172.26.0.1"

	data, err := os.ReadFile(filepath.Join(configDriveMount, ".nexus-gateway"))
	if err != nil {
		return defaultGW
	}
	if gw := strings.TrimSpace(string(data)); gw != "" {
		return gw
	}
	return defaultGW
}

func configureStaticGuestNetwork() error {
	macBytes, err := os.ReadFile("/sys/class/net/eth0/address")
	if err != nil {
		return fmt.Errorf("read eth0 mac address: %w", err)
	}

	gateway := readNexusGateway()
	ip, err := staticGuestIPForMAC(strings.TrimSpace(string(macBytes)), gateway)
	if err != nil {
		return err
	}

	if out, err := exec.Command("ip", "addr", "replace", ip+"/16", "dev", "eth0").CombinedOutput(); err != nil {
		return fmt.Errorf("ip addr replace %s/16 dev eth0: %w: %s", ip, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("ip", "route", "replace", "default", "via", gateway, "dev", "eth0").CombinedOutput(); err != nil {
		return fmt.Errorf("ip route replace default via %s dev eth0: %w: %s", gateway, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// staticGuestIPForMAC derives a guest IP in the same /16 subnet as the gateway,
// using the last two MAC octets as the host portion.
// For gateway "172.20.0.1" and MAC "aa:bb:cc:dd:12:34" → "172.20.18.52".
func staticGuestIPForMAC(mac, gateway string) (string, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(mac)), ":")
	if len(parts) != 6 {
		return "", fmt.Errorf("invalid mac address format: %q", mac)
	}

	b4, err := strconv.ParseUint(parts[4], 16, 8)
	if err != nil {
		return "", fmt.Errorf("parse mac byte 5: %w", err)
	}
	b5, err := strconv.ParseUint(parts[5], 16, 8)
	if err != nil {
		return "", fmt.Errorf("parse mac byte 6: %w", err)
	}

	// Derive subnet prefix from gateway (first two octets of "a.b.c.d").
	gwParts := strings.SplitN(gateway, ".", 4)
	if len(gwParts) < 2 {
		return fmt.Sprintf("172.26.%d.%d", b4, b5), nil
	}
	return fmt.Sprintf("%s.%s.%d.%d", gwParts[0], gwParts[1], b4, b5), nil
}

// setupDNSPath is the path to /etc/resolv.conf. Overridable in tests.
var setupDNSPath = "/etc/resolv.conf"

// setupDNS writes /etc/resolv.conf with public DNS servers if it is empty or
// missing. This is needed because the kernel ip= cmdline arg configures the
// network interface but does not create /etc/resolv.conf.
func setupDNS() {
	const content = "nameserver 8.8.8.8\nnameserver 1.1.1.1\n"

	// Prefer host-provided resolver config when present and valid.
	if data, err := os.ReadFile(setupDNSPath); err == nil && dnsConfigLooksUsable(string(data)) {
		return
	}

	_ = os.MkdirAll("/etc", 0o755)
	_ = os.WriteFile(setupDNSPath, []byte(content), 0o644)
}

func dnsConfigLooksUsable(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "nameserver" {
			// Loopback resolvers (127.x.x.x) are host-only stubs like
			// systemd-resolved that don't exist inside the VM.
			if !strings.HasPrefix(fields[1], "127.") {
				return true
			}
		}
	}
	return false
}

func resolveListener() (net.Listener, string, error) {
	if os.Getpid() == 1 || os.Getenv("AGENT_REQUIRE_VSOCK") == "1" {
		var lastErr error
		for attempt := 1; attempt <= 120; attempt++ {
			listener, err := listenVsock()
			if err == nil {
				emitDiagnostic("agent vsock listener ready after %d attempt(s)", attempt)
				return listener, "vsock", nil
			}
			lastErr = err
			if attempt == 1 || attempt%20 == 0 {
				emitDiagnostic("agent vsock listen attempt %d failed: %v", attempt, err)
			}
			time.Sleep(500 * time.Millisecond)
		}
		return nil, "", fmt.Errorf("listen vsock (required) failed: %w", lastErr)
	}

	if os.Getenv("AGENT_FORCE_TCP") == "1" {
		listener, err := listenTCP()
		return listener, "tcp", err
	}

	listener, err := listenVsock()
	if err == nil {
		return listener, "vsock", nil
	}

	tcpListener, tcpErr := listenTCP()
	if tcpErr != nil {
		return nil, "", fmt.Errorf("listen vsock: %w; listen tcp fallback: %v", err, tcpErr)
	}
	return tcpListener, "tcp-fallback", nil
}

func listenTCP() (net.Listener, error) {
	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = "8080"
	}
	return net.Listen("tcp", ":"+port)
}

func listenVsock() (net.Listener, error) {
	port := firecracker.DefaultAgentVSockPort
	if raw := strings.TrimSpace(os.Getenv("AGENT_VSOCK_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("invalid AGENT_VSOCK_PORT %q", raw)
		}
		port = uint32(parsed)
	}

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}

	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	if err := unix.Listen(fd, 128); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	file := os.NewFile(uintptr(fd), "vsock-listener")
	defer file.Close()

	listener, err := vsock.FileListener(file)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	return listener, nil
}

// startSSHAgentProxy creates /tmp/ssh-agent.sock and forwards each connection
// to the host SSH agent via vsock CID 2 (the hypervisor/host), port 10790.
// This lets git and ssh inside the VM use the host's SSH agent without any
// private keys being present in the VM filesystem.
func startSSHAgentProxy() {
	const (
		sockPath  = "/tmp/ssh-agent.sock"
		hostCID   = uint32(2) // VMADDR_CID_HOST
		agentPort = uint32(firecracker.VendingVSockPort)
	)

	_ = os.Setenv("SSH_AUTH_SOCK", sockPath)
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		emitDiagnostic("ssh-agent proxy: listen %s: %v", sockPath, err)
		return
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		emitDiagnostic("ssh-agent proxy: chmod: %v", err)
	}
	emitDiagnostic("ssh-agent proxy: listening on %s → vsock host port %d", sockPath, agentPort)

	go func() {
		defer ln.Close()
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				host, err := vsock.Dial(hostCID, agentPort, nil)
				if err != nil {
					emitDiagnostic("ssh-agent proxy: vsock dial host:%d: %v", agentPort, err)
					return
				}
				defer host.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(host, c); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, host); done <- struct{}{} }()
				<-done
			}(client)
		}
	}()
}

func emitDiagnostic(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)

	if console, err := os.OpenFile("/dev/console", os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_, _ = fmt.Fprintln(console, msg)
		_ = console.Close()
	}

	if kmsg, err := os.OpenFile("/dev/kmsg", os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_, _ = fmt.Fprintf(kmsg, "<6>nexus-firecracker-agent: %s\n", msg)
		_ = kmsg.Close()
	}
}
