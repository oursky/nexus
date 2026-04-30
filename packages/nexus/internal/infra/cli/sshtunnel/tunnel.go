package sshtunnel

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Manager manages an SSH port-forward tunnel.
type Manager struct {
	host         string
	remoteHost   string
	sshPort      int
	remotePort   int
	localPort    int
	verbose      bool
	identityFile string

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

// New creates a new tunnel manager.
// host is the SSH target (e.g. "newman@linuxbox").
// remotePort is the port the daemon listens on remotely.
// sshPort is the SSH port (0 = default 22).
func New(host string, remotePort, sshPort int) *Manager {
	return &Manager{
		host:       host,
		remoteHost: "localhost",
		sshPort:    sshPort,
		remotePort: remotePort,
	}
}

// NewVerbose creates a tunnel manager that prints the SSH command and passes -v.
func NewVerbose(host string, remotePort, sshPort int) *Manager {
	m := New(host, remotePort, sshPort)
	m.verbose = true
	return m
}

// NewWithOptions creates a tunnel manager with full options.
type Options struct {
	Verbose      bool
	IdentityFile string
}

func NewWithOptions(host string, remotePort, sshPort int, opts Options) *Manager {
	m := New(host, remotePort, sshPort)
	m.verbose = opts.Verbose
	m.identityFile = opts.IdentityFile
	return m
}

// NewWithTarget creates a tunnel manager that forwards to a specific remote host:port.
func NewWithTarget(host, remoteHost string, remotePort, sshPort int) *Manager {
	if strings.TrimSpace(remoteHost) == "" {
		remoteHost = "localhost"
	}
	return &Manager{
		host:       host,
		remoteHost: remoteHost,
		sshPort:    sshPort,
		remotePort: remotePort,
	}
}

// Ensure opens the SSH tunnel if not already running.
// Returns the local port the tunnel is listening on.
// If localPort is 0, it auto-selects an available port.
func (m *Manager) Ensure() (int, error) {
	return m.EnsureWithLocalPort(m.localPort)
}

// EnsureWithLocalPort opens the SSH tunnel if not already running,
// binding to the specified localPort. If localPort is 0, auto-selects.
func (m *Manager) EnsureWithLocalPort(localPort int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running && m.isAlive() {
		return m.localPort, nil
	}

	if localPort == 0 {
		port, err := findAvailablePort()
		if err != nil {
			return 0, fmt.Errorf("find available port: %w", err)
		}
		m.localPort = port
	} else {
		m.localPort = localPort
	}

	var args []string
	if m.identityFile != "" {
		// -F /dev/null prevents ~/.ssh/config from injecting additional keys.
		// IdentitiesOnly=yes + IdentityAgent=none ensure only the specified key is used.
		args = append(args,
			"-F", "/dev/null",
			"-i", m.identityFile,
			"-o", "IdentitiesOnly=yes",
			"-o", "IdentityAgent=none",
		)
	} else if id := strings.TrimSpace(os.Getenv("NEXUS_E2E_SSH_IDENTITY")); id != "" {
		args = append(args, "-i", id, "-o", "IdentitiesOnly=yes")
	}
	if os.Getenv("CI") != "" {
		args = append(args,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "BatchMode=yes",
		)
	}
	if m.verbose {
		args = append(args, "-v")
	}
	args = append(args, "-fNL",
		fmt.Sprintf("localhost:%d:%s:%d", m.localPort, m.remoteHost, m.remotePort),
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
	)
	if m.sshPort > 0 && m.sshPort != 22 {
		args = append([]string{"-p", strconv.Itoa(m.sshPort)}, args...)
	}
	args = append(args, m.host)

	cmd := exec.Command("ssh", args...)
	cmd.Stdin = nil
	if m.verbose {
		fmt.Fprintf(os.Stderr, "[nexus] ssh tunnel: ssh %s\n", strings.Join(args, " "))
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = nil
		cmd.Stderr = nil
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("ssh tunnel start: %w", err)
	}
	m.cmd = cmd
	m.running = true

	if err := m.waitForTunnel(3 * time.Second); err != nil {
		m.running = false
		return 0, fmt.Errorf("ssh tunnel not ready: %w", err)
	}

	return m.localPort, nil
}

// Close kills the SSH tunnel process.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
	}
	m.running = false
	return nil
}

// IsRunning returns whether the tunnel is currently active.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running && m.isAlive()
}

// LocalPort returns the local port the tunnel is listening on.
func (m *Manager) LocalPort() int {
	return m.localPort
}

func (m *Manager) isAlive() bool {
	if m.cmd == nil || m.cmd.Process == nil {
		return false
	}
	err := m.cmd.Process.Signal(os.Signal(nil))
	return err == nil
}

func (m *Manager) waitForTunnel(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", m.localPort), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("tunnel did not become ready within %s", timeout)
}

func findAvailablePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	ln, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
