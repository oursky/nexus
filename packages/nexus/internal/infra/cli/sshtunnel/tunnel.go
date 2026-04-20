package sshtunnel

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Manager manages an SSH port-forward tunnel.
type Manager struct {
	host       string
	sshPort    int
	remotePort int
	localPort  int

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
		sshPort:    sshPort,
		remotePort: remotePort,
	}
}

// Ensure opens the SSH tunnel if not already running.
// Returns the local port the tunnel is listening on.
// If localPort is 0, it auto-selects an available port.
func (m *Manager) Ensure() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running && m.isAlive() {
		return m.localPort, nil
	}

	if m.localPort == 0 {
		port, err := findAvailablePort()
		if err != nil {
			return 0, fmt.Errorf("find available port: %w", err)
		}
		m.localPort = port
	}

	args := []string{"-fNL", fmt.Sprintf("localhost:%d:localhost:%d", m.localPort, m.remotePort)}
	if m.sshPort > 0 && m.sshPort != 22 {
		args = append([]string{"-p", strconv.Itoa(m.sshPort)}, args...)
	}
	args = append(args, m.host, "sleep", "300")

	cmd := exec.Command("ssh", args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

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
