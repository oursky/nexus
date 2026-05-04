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
	args = append(args, "-NL",
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
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, fmt.Errorf("ssh tunnel open devnull: %w", err)
	}
	defer devNull.Close()
	if m.verbose {
		fmt.Fprintf(os.Stderr, "[nexus] ssh tunnel: ssh %s\n", strings.Join(args, " "))
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
		// Send SIGTERM first so SSH can send TCP FINs to any open forwarded
		// connections (e.g. browser keep-alive). SIGKILL would RST them immediately,
		// causing "connection reset" errors in the browser on the next request.
		_ = m.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			_ = m.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			// Exited cleanly.
		case <-time.After(300 * time.Millisecond):
			// Didn't exit — force kill.
			_ = m.cmd.Process.Kill()
			_ = m.cmd.Wait()
		}
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

// PID returns the OS process ID of the SSH tunnel child, or 0 if not running.
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil {
		return m.cmd.Process.Pid
	}
	return 0
}

func (m *Manager) isAlive() bool {
	if m.cmd == nil || m.cmd.Process == nil {
		return false
	}
	err := m.cmd.Process.Signal(os.Signal(nil))
	return err == nil
}

// waitForTunnel waits until the SSH tunnel is ready to forward connections.
//
// SSH opens the local listener socket before completing authentication. A
// connection to localhost:localPort at that point is accepted by SSH locally
// but immediately RST'd when SSH tries to open the forwarding channel. We
// detect "truly ready" by dialing and attempting a 1-byte read with a short
// deadline: an authenticated tunnel holds the connection open (read times out),
// whereas a not-yet-authenticated tunnel RST's it immediately (read returns
// an error with 0 bytes in < a few ms).
func (m *Manager) waitForTunnel(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Phase 1: wait for the local SSH listener to bind.
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", m.localPort), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
		if time.Now().After(deadline) {
			return fmt.Errorf("tunnel local listener did not bind within %s", timeout)
		}
	}

	// Phase 2: wait until the tunnel actually forwards (SSH session authenticated).
	// Probe by dialing and doing a short-deadline read. If the read times out,
	// the connection is being held (forwarding ready). If it errors immediately,
	// SSH closed it (auth not done yet).
	const probeReadTimeout = 150 * time.Millisecond
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", m.localPort), 300*time.Millisecond)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(probeReadTimeout))
		buf := make([]byte, 1)
		_, readErr := conn.Read(buf)
		_ = conn.Close()
		if readErr != nil {
			if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
				// Read timed out = connection held open = tunnel is forwarding.
				return nil
			}
		}
		// Connection was closed/RST immediately — SSH not ready yet.
		time.Sleep(50 * time.Millisecond)
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
