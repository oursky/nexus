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
	"golang.org/x/sys/unix"
)

// vsock port constants shared with the host-side runtime drivers.
const (
	defaultAgentVSockPort    uint32 = 10789
	defaultSpotlightVSockPort uint32 = 10792
	vendingVSockPort         uint32 = 10790
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

	// virtiofsWorkspaceOnce ensures setupVirtiofsWorkspace runs exactly once.
	// Unlike ext4 block devices (which return EBUSY on re-mount), overlayfs
	// silently stacks a new mount on each call, so an idempotency check via
	// the mount return code is not reliable.
	virtiofsWorkspaceOnce    sync.Once
	virtiofsWorkspaceOnceErr error
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

// isBakeMode reports whether the agent is running in rootfs-bake mode.
// In bake mode the agent installs all tools synchronously then powers off
// the VM so the host can use the resulting rootfs as the pre-baked base.
func isBakeMode() bool {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == "nexus.bake=1" {
			return true
		}
	}
	return false
}

func main() {
	pid := os.Getpid()
	emitDiagnostic("agent boot pid=%d", pid)

	bootstrapGuestEnvironment(pid)

	// Bake mode: install all tools synchronously, then power off.
	// The host waits for the VM process to exit and uses the resulting
	// rootfs.ext4 as the pre-baked base image for all future workspaces.
	if isBakeMode() {
		emitDiagnostic("agent bake: starting rootfs pre-bake")
		ensureGuestBasePackages()
		ensureGuestCLITools()
		emitDiagnostic("agent bake: all tools installed — powering off VM")
		// Give the serial console a moment to flush.
		time.Sleep(500 * time.Millisecond)
		_ = unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
		os.Exit(0) // unreachable, but satisfies the compiler
	}

	startSSHAgentProxy()

	// Install the OS developer toolchain synchronously before accepting PTY
	// connections.  The workspace stays in "starting" state on the host side
	// until we open the vsock listener, so the user never lands in a shell
	// without make/node/docker available.
	//
	// On subsequent boots the stamp file makes this a no-op (<1 s).
	ensureGuestBasePackages()
	// CLI tools (opencode/codex/claude) can install in the background — they
	// are not required before a user can open a terminal.
	go ensureGuestCLITools()
	// Docker daemon also starts in the background.
	// In libkrun container mode the agent is not PID 1 but NEXUS_CONTAINER_MODE
	// tells us to start Docker anyway.
	if isPrimaryAgent() {
		go func() {
			if err := ensureDockerDaemon(); err != nil {
				emitDiagnostic("agent docker daemon setup failed (non-fatal): %v", err)
			}
		}()
	}

	go startSpotlightListener()

	listener, transport, err := resolveListener()
	if err != nil {
		emitDiagnostic("agent listener setup failed: %v", err)
		log.Fatalf("Failed to listen: %v", err)
	}
	defer listener.Close()

	emitDiagnostic("agent listener ready transport=%s", transport)
	log.Printf("nexus guest agent listening (%s)", transport)

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
	port := defaultSpotlightVSockPort

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

	primary := isPrimaryAgent()
	if primary {
		mountKernelFilesystemsFunc()
		if err := setupPTYDevices(); err != nil {
			emitDiagnostic("agent pty device setup failed (non-fatal): %v", err)
		}
		emitDiagnostic("agent primary kernel filesystems mounted (pid=%d container_mode=%v)", pid, os.Getenv("NEXUS_CONTAINER_MODE") == "1")
	}

	if err := setupWorkspaceMountFunc(); err != nil {
		emitDiagnostic("agent workspace mount failed (non-fatal): %v", err)
	} else {
		emitDiagnostic("agent workspace mounted")
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

	if primary {
		maybeStartSSHDInGuest()
	}

	setupDNSFunc()
	emitDiagnostic("agent dns configured")
}

const hostConfigMount = "/run/nexus-host"

// applyHostConfigDrive mounts the host config ext4 image at /run/nexus-host
// and copies known config files into place.
// Device path is resolved from NEXUS_CONFIG_DEV (libkrun) or derived from mode
// (virtiofs layout → /dev/vdd, legacy block layout → /dev/vdc).
func applyHostConfigDrive() error {
	hostConfigDevice := configDevPath()
	if _, err := os.Stat(hostConfigDevice); err != nil {
		return nil // drive not attached — nothing to do
	}

	if err := os.MkdirAll(hostConfigMount, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", hostConfigMount, err)
	}

	if err := unix.Mount(hostConfigDevice, hostConfigMount, "ext4", unix.MS_RDONLY, ""); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount %s: %w", hostConfigDevice, err)
		}
		// Already mounted from a previous call — proceed to copy files.
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
		// Codex CLI OAuth token.
		{".codex/auth.json", "/root/.codex/auth.json", 0o600},
		{".codex/config.json", "/root/.codex/config.json", 0o644},
		// opencode provider OAuth tokens (Copilot, OpenAI, etc.).
		{".local/share/opencode/auth.json", "/root/.local/share/opencode/auth.json", 0o600},
	}

	// Ensure /root and /root/.ssh are owned by uid 0 (root). The rootfs may
	// have been built by a non-root unsquashfs extraction which assigns the
	// builder's UID to all files. SSHd refuses to honour authorized_keys
	// inside a directory not owned by the authenticating user.
	_ = os.MkdirAll("/root/.ssh", 0o700)
	_ = os.Chown("/root", 0, 0)
	_ = os.Chown("/root/.ssh", 0, 0)

	for _, c := range copies {
		src := filepath.Join(hostConfigMount, c.src)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // file not in image — skip
		}
		if err := os.MkdirAll(filepath.Dir(c.dst), 0o755); err != nil {
			continue
		}
		// Remove any broken symlink at the destination before writing
		// (Ubuntu minimal cloud image ships /etc/resolv.conf as a dangling
		// symlink; os.WriteFile would silently fail through it).
		if fi, lerr := os.Lstat(c.dst); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(c.dst)
		}
		_ = os.WriteFile(c.dst, data, c.mode)
	}

	// Ensure git safe.directory for the workspace.
	_ = exec.Command("git", "config", "--global", "--add", "safe.directory", "/workspace").Run()
	_ = exec.Command("git", "config", "--global", "--add", "safe.directory", "*").Run()
	// Strip platform-specific credential helpers that won't work in the VM.
	_ = exec.Command("git", "config", "--global", "--unset-all", "credential.helper").Run()

	// Write SSH_AUTH_SOCK and API key env vars to profile so all shells pick
	// them up.  .nexus-env contains export statements for AI/LLM API keys
	// collected from the daemon host's environment.
	profileLines := "\nexport SSH_AUTH_SOCK=/tmp/ssh-agent.sock\n"
	profileLines += "[ -f /run/nexus-host/.nexus-env ] && . /run/nexus-host/.nexus-env\n"
	f, err := os.OpenFile("/root/.profile", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(profileLines)
		f.Close()
	}

	// Also source immediately into the current process environment so that
	// docker, npm install, and background services inherit the keys.
	if envData, err := os.ReadFile(filepath.Join(hostConfigMount, ".nexus-env")); err == nil {
		for _, line := range strings.Split(string(envData), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "export ") {
				continue
			}
			kv := strings.TrimPrefix(line, "export ")
			idx := strings.IndexByte(kv, '=')
			if idx < 0 {
				continue
			}
			key := kv[:idx]
			val := kv[idx+1:]
			// Strip surrounding single quotes added by shell quoting.
			if len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'' {
				val = strings.ReplaceAll(val[1:len(val)-1], "'\\''", "'")
			}
			_ = os.Setenv(key, val)
		}
	}

	return nil
}

// runStreamed runs a command with output wired directly to /dev/console so
// every byte appears in the hvc0 serial log without Go-level buffering.
// /dev/kmsg rate-limits burst writes, so we bypass emitDiagnostic here and
// write the header/trailer via emitDiagnostic only (two lines total).
func runStreamed(ctx context.Context, env []string, prefix, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}

	// Attach stdout/stderr directly to the serial console so output is
	// visible in real time without buffering.
	console, consErr := os.OpenFile("/dev/console", os.O_WRONLY|os.O_APPEND, 0)
	if consErr == nil {
		cmd.Stdout = console
		cmd.Stderr = console
	}

	if err := cmd.Start(); err != nil {
		if console != nil {
			_ = console.Close()
		}
		return fmt.Errorf("start %s: %w", name, err)
	}

	err := cmd.Wait()
	if console != nil {
		_ = console.Close()
	}
	if err != nil {
		return fmt.Errorf("%s exited: %w", name, err)
	}
	return nil
}

// ensureGuestBasePackages installs the developer toolchain (make, nodejs, npm,
// docker.io, build-essential) via apt-get inside the VM.  The VM is a real root
// environment so apt-get works without any user-namespace restrictions.
//
// A versioned stamp file in the rootfs prevents
// re-running on every workspace start — packages only install once per rootfs.
func ensureGuestBasePackages() {
	const stampFile = "/var/lib/nexus-tools-installed-v4"
	if _, err := os.Stat(stampFile); err == nil {
		emitDiagnostic("agent base packages: already installed (stamp found)")
		return
	}

	if _, err := exec.LookPath("apt-get"); err != nil {
		emitDiagnostic("agent base packages: apt-get not found, skipping")
		return
	}

	emitDiagnostic("agent base packages: installing (make, nodejs, npm, docker.io, build-essential, git, busybox-static)...")

	pkgs := []string{
		"make",
		"build-essential",
		"nodejs",
		"npm",
		"docker.io",
		"containerd",
		"git",
		"curl",
		"wget",
		"python3",
		"python3-pip",
		// busybox-static provides udhcpc (DHCP client) used by the agent's
		// setupNetwork to acquire an IP from passt at VM boot time.
		"busybox-static",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		// virtiofs rootfs: chown(uid=0) fails because the host kernel rejects
		// it from a non-root process.  --no-same-owner tells tar (used by
		// dpkg-deb) to skip ownership restoration during package extraction.
		"TAR_OPTIONS=--no-same-owner",
	)

	// Some minimal rootfs images omit /var/log/apt/ and /var/log/dpkg.log,
	// which causes apt-get to exit 100 with "E: Directory '/var/log/apt/' missing"
	// even when packages install successfully.  Create them upfront so apt is happy.
	for _, dir := range []string{"/var/log/apt", "/var/cache/apt/archives/partial"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			emitDiagnostic("agent base packages: mkdir %s failed: %v", dir, err)
		}
	}
	if _, err := os.Stat("/var/log/dpkg.log"); os.IsNotExist(err) {
		if f, err := os.OpenFile("/var/log/dpkg.log", os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
		}
	}

	emitDiagnostic("agent base packages: running apt-get update...")
	if err := runStreamed(ctx, env, "apt-get update", "apt-get", "update"); err != nil {
		emitDiagnostic("agent base packages: apt-get update FAILED: %v", err)
		return
	}

	emitDiagnostic("agent base packages: running apt-get install...")
	installArgs := append([]string{"install", "-y", "--no-install-recommends"}, pkgs...)
	if err := runStreamed(ctx, env, "apt-get install", "apt-get", installArgs...); err != nil {
		emitDiagnostic("agent base packages: apt-get install FAILED: %v", err)
		return
	}

	// Install docker compose plugin (v2) from the GitHub release.
	// Ubuntu's default repos only ship docker.io which has no compose plugin;
	// Docker's official CLI plugin binary must be installed separately.
	installDockerComposePlugin(ctx, env)

	// Write stamp so subsequent boots skip this step.
	_ = os.MkdirAll("/var/lib", 0o755)
	_ = os.WriteFile(stampFile, []byte("ok\n"), 0o644)
	emitDiagnostic("agent base packages: installed successfully")
}

// installDockerComposePlugin installs the Docker Compose v2 CLI plugin binary
// directly from the GitHub release.  Ubuntu's default repos ship docker.io
// which does not include the compose plugin; this is the canonical way to add
// `docker compose` support alongside the ubuntu-packaged docker.io.
func installDockerComposePlugin(ctx context.Context, env []string) {
	const pluginPath = "/usr/local/lib/docker/cli-plugins/docker-compose"

	if _, err := os.Stat(pluginPath); err == nil {
		emitDiagnostic("agent docker-compose-plugin: already installed")
		return
	}

	// Find the curl binary that was just installed.
	curlPath, err := exec.LookPath("curl")
	if err != nil {
		emitDiagnostic("agent docker-compose-plugin: curl not found, skipping: %v", err)
		return
	}

	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		emitDiagnostic("agent docker-compose-plugin: mkdir failed: %v", err)
		return
	}

	// Pin to a known-good version that is compatible with docker.io on Ubuntu 24.04.
	const composeVersion = "v2.24.6"
	url := fmt.Sprintf(
		"https://github.com/docker/compose/releases/download/%s/docker-compose-linux-x86_64",
		composeVersion,
	)
	emitDiagnostic("agent docker-compose-plugin: downloading %s...", composeVersion)

	tmpPath := pluginPath + ".tmp"
	if err := runStreamed(ctx, env, "curl docker-compose", curlPath, "-fSL", "--retry", "3", "--progress-bar", "-o", tmpPath, url); err != nil {
		_ = os.Remove(tmpPath)
		emitDiagnostic("agent docker-compose-plugin: download FAILED: %v", err)
		return
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		emitDiagnostic("agent docker-compose-plugin: chmod failed: %v", err)
		return
	}
	if err := os.Rename(tmpPath, pluginPath); err != nil {
		_ = os.Remove(tmpPath)
		emitDiagnostic("agent docker-compose-plugin: rename failed: %v", err)
		return
	}
	emitDiagnostic("agent docker-compose-plugin: installed %s at %s", composeVersion, pluginPath)
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
	if err != nil || strings.TrimSpace(npmPath) == "" {
		emitDiagnostic("agent cli tools: npm not found, skipping install")
		return
	}

	env := ensurePathInEnv(os.Environ())

	missing := make([]cliSpec, 0, len(tools))
	for _, tool := range tools {
		if _, err := lookPathInEnv(tool.binary, env); err == nil {
			emitDiagnostic("agent cli tools: %s already installed", tool.binary)
			continue
		}
		missing = append(missing, tool)
	}
	if len(missing) == 0 {
		emitDiagnostic("agent cli tools: all tools present")
		return
	}

	var packages []string
	for _, tool := range missing {
		packages = append(packages, tool.pkg)
		emitDiagnostic("agent cli tools: will install %s (%s)", tool.binary, tool.pkg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	args := append([]string{"install", "-g", "--force"}, packages...)
	emitDiagnostic("agent cli tools: running npm install -g %s...", strings.Join(packages, " "))
	if err := runStreamed(ctx, env, "npm install", npmPath, args...); err != nil {
		emitDiagnostic("agent cli tools: npm install FAILED: %v", err)
		return
	}

	// Verify each binary is now resolvable.
	envAfter := ensurePathInEnv(os.Environ())
	for _, tool := range missing {
		if _, err := lookPathInEnv(tool.binary, envAfter); err != nil {
			emitDiagnostic("agent cli tools: WARNING %s not found after install: %v", tool.binary, err)
		} else {
			emitDiagnostic("agent cli tools: %s installed OK", tool.binary)
		}
	}
}

func ensureAgentProcessPath() {
	if strings.TrimSpace(os.Getenv("PATH")) == "" {
		_ = os.Setenv("PATH", defaultAgentPath)
	}
}

// isVirtiofsWorkspaceMode reports whether the workspace is virtiofs+overlayfs.
// In libkrun container mode the kernel cmdline is not under our control, so
// the host sets NEXUS_WORKSPACE_MODE=virtiofs via krun_set_exec. Legacy
// virtiofs guests set nexus.workspace=virtiofs on the kernel cmdline.
func isVirtiofsWorkspaceMode() bool {
	if os.Getenv("NEXUS_WORKSPACE_MODE") == "virtiofs" {
		return true
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == "nexus.workspace=virtiofs" {
			return true
		}
	}
	return false
}

// isPrimaryAgent reports whether this agent instance should behave like PID 1
// (mounting kernel filesystems, starting sshd, starting Docker). In virtiofs
// the agent IS PID 1. In libkrun container mode the agent is launched by
// krun_set_exec and is NOT PID 1, but the host sets NEXUS_CONTAINER_MODE=1 to
// tell it to still perform these duties.
func isPrimaryAgent() bool {
	return os.Getpid() == 1 || os.Getenv("NEXUS_CONTAINER_MODE") == "1"
}

// overlayDevPath returns the block device for the workspace overlay upper layer.
// Default /dev/vdb (virtiofs mode); overridden to /dev/vda in
// libkrun container mode via NEXUS_OVERLAY_DEV.
func overlayDevPath() string {
	if v := os.Getenv("NEXUS_OVERLAY_DEV"); v != "" {
		return v
	}
	return "/dev/vdb"
}

// dockerDevPath returns the block device for docker-data.
// Default /dev/vdc (virtiofs mode); overridden in libkrun via NEXUS_DOCKER_DEV.
func dockerDevPath() string {
	if v := os.Getenv("NEXUS_DOCKER_DEV"); v != "" {
		return v
	}
	return "/dev/vdc"
}

// configDevPath returns the block device for the host config drive.
// Default /dev/vdd (virtiofs mode); overridden in libkrun via NEXUS_CONFIG_DEV.
func configDevPath() string {
	if v := os.Getenv("NEXUS_CONFIG_DEV"); v != "" {
		return v
	}
	return "/dev/vdd"
}

// setupVirtiofsWorkspace assembles the virtiofs+overlayfs workspace mount.
//
// Device layout is resolved from environment variables set by the host daemon:
//
//	virtiofs "nexus-workspace"   project dir (ro lower layer)
//	NEXUS_OVERLAY_DEV (/dev/vdb) workspace-overlay.ext4 (rw upper layer)
//	NEXUS_DOCKER_DEV  (/dev/vdc) docker-data.ext4 → /var/lib/docker
//	NEXUS_CONFIG_DEV  (/dev/vdd) hostconfig.ext4  → /run/nexus-host  (read-only)
//
// virtiofs mode falls back to /dev/vdb, /dev/vdc, /dev/vdd.
func setupVirtiofsWorkspace() error {
	overlayDev := overlayDevPath()
	dockerDev := dockerDevPath()

	lowerDir := "/mnt/nexus-lower"
	overlayMnt := "/mnt/nexus-overlay"

	for _, dir := range []string{lowerDir, overlayMnt, workspaceMountPoint, "/var/lib/docker"} {
		if err := workspaceMkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Mount virtiofs tag → lower dir (read-only project directory passthrough).
	if err := workspaceMountFunc("nexus-workspace", lowerDir, "virtiofs", unix.MS_RDONLY, ""); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount virtiofs nexus-workspace → %s: %w", lowerDir, err)
		}
	} else {
		emitDiagnostic("agent virtiofs lower mounted at %s", lowerDir)
	}

	// Wait for workspace-overlay ext4 device and mount it.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := workspaceStat(overlayDev); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("workspace overlay device %s not available after 30s", overlayDev)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := workspaceMountFunc(overlayDev, overlayMnt, "ext4", 0, ""); err != nil && !errors.Is(err, unix.EBUSY) {
		return fmt.Errorf("mount overlay ext4 %s → %s: %w", overlayDev, overlayMnt, err)
	}

	// Ensure upper and work directories exist inside the overlay ext4.
	upperDir := overlayMnt + "/upper"
	workDir := overlayMnt + "/work"
	for _, dir := range []string{upperDir, workDir} {
		if err := workspaceMkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir overlayfs dir %s: %w", dir, err)
		}
	}

	// Mount overlayfs: lower=virtiofs, upper=overlay ext4, merged → /workspace.
	overlayOpts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, upperDir, workDir)
	if err := workspaceMountFunc("overlay", workspaceMountPoint, "overlay", 0, overlayOpts); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount overlayfs → %s: %w", workspaceMountPoint, err)
		}
	} else {
		emitDiagnostic("agent overlayfs workspace mounted at %s", workspaceMountPoint)
	}

	// Wait for docker-data ext4 device and mount to /var/lib/docker.
	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, err := workspaceStat(dockerDev); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("docker-data device %s not available after 30s", dockerDev)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := workspaceMountFunc(dockerDev, "/var/lib/docker", "ext4", 0, ""); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount docker-data %s → /var/lib/docker: %w", dockerDev, err)
		}
	} else {
		emitDiagnostic("agent docker-data mounted at /var/lib/docker")
	}

	return nil
}

func setupWorkspaceMount() error {
	if isVirtiofsWorkspaceMode() {
		return setupVirtiofsWorkspaceOnce()
	}
	return setupWorkspaceMountWithRequirement(false)
}

func setupWorkspaceMountRequired() error {
	if isVirtiofsWorkspaceMode() {
		return setupVirtiofsWorkspaceOnce()
	}
	return setupWorkspaceMountWithRequirement(true)
}

// setupVirtiofsWorkspaceOnce calls setupVirtiofsWorkspace exactly once.
// Overlayfs does not return EBUSY on re-mount — it silently stacks a new
// mount — so idempotency must be enforced here rather than at the syscall.
func setupVirtiofsWorkspaceOnce() error {
	virtiofsWorkspaceOnce.Do(func() {
		virtiofsWorkspaceOnceErr = setupVirtiofsWorkspace()
	})
	return virtiofsWorkspaceOnceErr
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
	// Mount tmpfs at /tmp with sticky bit so all users (including apt's _apt
	// sandbox user) can create temp files.  Without this, apt-key fails trying
	// to write to /tmp which is only 0755 in the base squashfs.
	_ = kernelMkdirAll("/tmp", 0o1777)
	_ = kernelMountFunc("tmpfs", "/tmp", "tmpfs", 0, "mode=1777")
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

	// In virtiofs mode: Docker data lives on /dev/vdc (mounted at /var/lib/docker),
	// a dedicated sparse ext4 so overlay2 runs on a native kernel filesystem.
	// In legacy mode: data lives inside /workspace (native ext4 block device).
	dataRoot := "/workspace/.nexus-docker"
	execRoot := "/workspace/.nexus-docker-exec"
	if isVirtiofsWorkspaceMode() {
		dataRoot = "/var/lib/docker"
		execRoot = "/var/lib/docker-exec"
	}

	_ = os.Remove("/var/run/docker.pid")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir docker data root: %w", err)
	}
	if err := os.MkdirAll(execRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir docker exec root: %w", err)
	}

	// Older 5.10 CI kernels do not support nftables.  Ubuntu 24.04's
	// default `iptables` binary is nftables-backed and will fail with
	// "Failed to initialize nft: Protocol not supported".  Switch to
	// iptables-legacy (which ships alongside iptables in the iptables package)
	// so Docker can create its DOCKER NAT chain.
	iptablesFlag := "--iptables=false"
	if legacyPath, err := exec.LookPath("iptables-legacy"); err == nil {
		_ = exec.Command("update-alternatives", "--set", "iptables", legacyPath).Run()
		if legacy6Path, err := exec.LookPath("ip6tables-legacy"); err == nil {
			_ = exec.Command("update-alternatives", "--set", "ip6tables", legacy6Path).Run()
		}
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

// setupNetwork brings up eth0 via DHCP (udhcpc or dhcpcd) or static fallback.
// It is called at PID1 boot before setupDNS so that DNS resolution works.
func setupNetwork() error {
	return setupNetworkFunc()
}

// maybeStartSSHDInGuest launches OpenSSH sshd when the rootfs provides it.
// The rootfs is built by nexus daemon bootstrap which installs openssh-server,
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

	// Run sshd in foreground+stderr mode so its output appears in the hvc0
	// serial log. sshd's -D keeps it in the foreground, -e logs to stderr.
	// We open /dev/console directly so the output appears in the guest serial
	// log regardless of what fd 2 (stderr) is connected to in the agent.
	consoleF, _ := os.OpenFile("/dev/console", os.O_WRONLY|os.O_APPEND, 0)
	if consoleF == nil {
		consoleF = os.Stderr
	}
	cmd := exec.Command(sshdPath, "-D", "-e")
	cmd.Stdout = consoleF
	cmd.Stderr = consoleF
	if err := cmd.Start(); err != nil {
		_ = consoleF.Close()
		emitDiagnostic("sshd: start failed (non-fatal): %v", err)
		return
	}
	_ = consoleF.Close() // child inherited the fd; parent can close its copy
	emitDiagnostic("sshd: started (pid=%d) foreground+stderr mode", cmd.Process.Pid)
	// Reap sshd if it exits unexpectedly so it doesn't become a zombie.
	go func() {
		if err := cmd.Wait(); err != nil {
			emitDiagnostic("sshd: exited unexpectedly: %v", err)
		} else {
			emitDiagnostic("sshd: exited cleanly")
		}
	}()
}

func realSetupNetwork() error {
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set lo up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Bring the interface up first so DHCP can configure it.
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
			if hasUsableIPv4OnEth0() {
				return nil
			}
			emitDiagnostic("udhcpc exited successfully but eth0 has no usable IPv4; falling through to other DHCP/static methods")
		}
		emitDiagnostic("udhcpc failed: %v: %s", err, strings.TrimSpace(string(out)))
	} else if _, err := exec.LookPath("dhcpcd"); err == nil {
		// dhcpcd is available (installed on Ubuntu when busybox is not).
		// -1 (--oneshot): exit after the first interface is configured.
		// This makes dhcpcd behave like a one-shot DHCP client.
		out, err := exec.Command("dhcpcd", "-1", "eth0").CombinedOutput()
		if err == nil {
			if hasUsableIPv4OnEth0() {
				return nil
			}
			emitDiagnostic("dhcpcd exited successfully but eth0 has no usable IPv4; falling through to static fallback")
		}
		emitDiagnostic("dhcpcd failed: %v: %s", err, strings.TrimSpace(string(out)))
	} else if _, err := exec.LookPath("dhclient"); err == nil {
		out, err := exec.Command("dhclient", "-1", "-v", "eth0").CombinedOutput()
		if err == nil {
			if hasUsableIPv4OnEth0() {
				return nil
			}
			emitDiagnostic("dhclient exited successfully but eth0 has no usable IPv4; falling through to static fallback")
		}
		emitDiagnostic("dhclient failed: %v: %s", err, strings.TrimSpace(string(out)))
	}

	emitDiagnostic("all DHCP clients failed or unavailable, falling back to static guest IP")
	if err := configureStaticGuestNetwork(); err != nil {
		return fmt.Errorf("static network fallback failed: %w", err)
	}
	return nil
}

// hasUsableIPv4OnEth0 reports whether eth0 has a global IPv4 address and a
// default route. Some DHCP clients can exit 0 after configuring only IPv6,
// which leaves host->guest TCP forwarding broken for passt/libkrun SSH.
func hasUsableIPv4OnEth0() bool {
	addrOut, addrErr := exec.Command("sh", "-lc", "ip -4 addr show dev eth0 scope global | grep -q 'inet '").CombinedOutput()
	routeOut, routeErr := exec.Command("sh", "-lc", "ip route show default | grep -q '^default '").CombinedOutput()
	if addrErr == nil && routeErr == nil {
		return true
	}
	emitDiagnostic(
		"network verify: missing IPv4 or default route after DHCP (addrErr=%v routeErr=%v addr=%q route=%q)",
		addrErr,
		routeErr,
		strings.TrimSpace(string(addrOut)),
		strings.TrimSpace(string(routeOut)),
	)
	return false
}

// readNexusGateway returns the guest's default gateway IP. It checks, in order:
//  1. The host config drive (.nexus-gateway) — set for normal workspace VMs.
//  2. The kernel cmdline nexus.gw= parameter — set for bake VMs.
//  3. A compiled-in default (172.26.0.1) which passt's static-IP fallback uses.
func readNexusGateway() string {
	const configDriveMount = "/run/nexus-host"
	const defaultGW = "172.26.0.1"

	// 1. Host config drive (workspace VMs).
	if data, err := os.ReadFile(filepath.Join(configDriveMount, ".nexus-gateway")); err == nil {
		if gw := strings.TrimSpace(string(data)); gw != "" {
			return gw
		}
	}

	// 2. Kernel cmdline nexus.gw= (bake VMs and any VM without a config drive).
	if cmdline, err := os.ReadFile("/proc/cmdline"); err == nil {
		for _, field := range strings.Fields(string(cmdline)) {
			if strings.HasPrefix(field, "nexus.gw=") {
				if gw := strings.TrimPrefix(field, "nexus.gw="); gw != "" {
					return gw
				}
			}
		}
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
//
// In libkrun TSI mode (no eth0) DNS UDP packets have no interface to route
// through, but TSI does intercept TCP sockets. "options use-vc" forces
// glibc's resolver to use TCP for all DNS queries so they transit TSI.
func setupDNS() {
	tsiMode := isTSINetworkMode()
	var content string
	if tsiMode {
		// TSI: force DNS over TCP (use-vc) so queries go through TSI, not UDP.
		content = "nameserver 8.8.8.8\nnameserver 1.1.1.1\noptions use-vc\n"
	} else {
		content = "nameserver 8.8.8.8\nnameserver 1.1.1.1\n"
	}

	// Prefer host-provided resolver config when present and valid (real file,
	// not a broken symlink). Always ensure use-vc is set in TSI mode.
	if fi, statErr := os.Lstat(setupDNSPath); statErr == nil && fi.Mode()&os.ModeSymlink == 0 {
		if data, err := os.ReadFile(setupDNSPath); err == nil && dnsConfigLooksUsable(string(data)) {
			if !tsiMode {
				return
			}
			// In TSI mode: patch existing config to add use-vc if not present.
			existing := string(data)
			if !strings.Contains(existing, "use-vc") {
				existing = strings.TrimRight(existing, "\n") + "\noptions use-vc\n"
				_ = os.WriteFile(setupDNSPath, []byte(existing), 0o644)
			}
			return
		}
	}

	_ = os.MkdirAll("/etc", 0o755)
	// Remove any broken symlink first (Ubuntu minimal cloud image ships
	// /etc/resolv.conf → ../run/systemd/resolve/stub-resolv.conf which
	// points to a path that doesn't exist inside the VM). os.WriteFile
	// follows symlinks, so it would silently fail against the missing target;
	// unlinking first ensures we create a real file at the path.
	_ = os.Remove(setupDNSPath)
	_ = os.WriteFile(setupDNSPath, []byte(content), 0o644)
}

func isTSINetworkMode() bool {
	if isVirtiofsWorkspaceMode() {
		return true
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == "nexus.tsi=1" {
			return true
		}
	}
	return false
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
	port := defaultAgentVSockPort
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
		agentPort = vendingVSockPort
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
		_, _ = fmt.Fprintf(kmsg, "<6>nexus-guest-agent: %s\n", msg)
		_ = kmsg.Close()
	}
}
