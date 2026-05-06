package sshtunnel

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Forward describes a single port-forward spec: localPort → remoteHost:remotePort.
// If LocalPort is 0 the OS selects a free port.
type Forward struct {
	LocalPort  int
	RemoteHost string
	RemotePort int
}

// MultiTunnel manages a single SSH process with multiple -L port-forward specs.
// Using one process means cleanup is a single signal and avoids PID lists.
type MultiTunnel struct {
	host         string
	sshPort      int
	identityFile string
	verbose      bool

	mu         sync.Mutex
	cmd        *exec.Cmd
	localPorts []int // resolved local ports in the same order as the Forwards passed to Start
}

// NewMulti creates a MultiTunnel for the given SSH target.
func NewMulti(host string, sshPort int) *MultiTunnel {
	return &MultiTunnel{host: host, sshPort: sshPort}
}

// NewMultiWithOptions creates a MultiTunnel with extra SSH options.
func NewMultiWithOptions(host string, sshPort int, identityFile string, verbose bool) *MultiTunnel {
	return &MultiTunnel{host: host, sshPort: sshPort, identityFile: identityFile, verbose: verbose}
}

// Start spawns a single SSH process forwarding all given specs.
// Returns the resolved local ports (one per Forward, same order).
// Blocks until all ports are bound and the tunnel is verified ready.
func (m *MultiTunnel) Start(fwds []Forward, timeout time.Duration) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return nil, fmt.Errorf("MultiTunnel already started")
	}

	// Resolve free ports for any Forward with LocalPort==0.
	resolved := make([]int, len(fwds))
	for i, f := range fwds {
		if f.LocalPort != 0 {
			resolved[i] = f.LocalPort
			continue
		}
		p, err := findAvailablePort()
		if err != nil {
			return nil, fmt.Errorf("find available port: %w", err)
		}
		resolved[i] = p
	}

	// Evict any stale processes (e.g. orphaned SSH tunnels) that are
	// already listening on the ports we intend to bind. Without this step
	// the new SSH process fails with ExitOnForwardFailure and the caller
	// sees "tunnel did not become ready".
	if evictPortHolders(resolved) {
		// Brief pause so the OS releases the port sockets before SSH tries to bind.
		time.Sleep(300 * time.Millisecond)
	}

	args := m.buildArgs(fwds, resolved)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = nil
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, fmt.Errorf("open devnull: %w", err)
	}
	defer devNull.Close()
	cmd.Stdout = devNull
	if m.verbose {
		cmd.Stderr = os.Stderr
		fmt.Fprintf(os.Stderr, "[nexus] ssh multi-tunnel: ssh %s\n", strings.Join(args, " "))
	} else {
		cmd.Stderr = devNull
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ssh multi-tunnel start: %w", err)
	}
	m.cmd = cmd
	m.localPorts = resolved

	if err := m.waitReady(resolved, timeout); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		m.cmd = nil
		m.localPorts = nil
		return nil, err
	}

	return resolved, nil
}

// PID returns the SSH process PID, or 0 if not started.
func (m *MultiTunnel) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil {
		return m.cmd.Process.Pid
	}
	return 0
}

// Close shuts down the SSH process gracefully (SIGTERM → wait 300ms → SIGKILL).
// A graceful exit lets SSH send TCP FINs to open forwarded connections (browser
// keep-alives), preventing "connection reset" errors on the next request.
func (m *MultiTunnel) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}

	_ = m.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = m.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
	}
	m.cmd = nil
	m.localPorts = nil
	return nil
}

// CloseByPID sends SIGTERM to a previously-persisted SSH PID (cross-process cleanup).
// Falls back to SIGKILL after 300ms. No-op if the process is already gone.
func CloseByPID(pid int) {
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(os.Interrupt)
	// Give the process a moment to exit cleanly.
	time.Sleep(300 * time.Millisecond)
	// Best-effort SIGKILL in case it didn't exit.
	_ = proc.Kill()
}

// CloseListenersOnPorts best-effort terminates processes listening on the
// provided local TCP ports. Returns true if any process was evicted.
func CloseListenersOnPorts(ports []int) bool {
	return evictPortHolders(ports)
}

// evictPortHolders finds any processes listening on the given local TCP ports
// and sends them SIGTERM (then SIGKILL). This clears stale SSH tunnels that
// were orphaned by a previous session so the new tunnel can bind the same ports.
// Returns true if any processes were evicted.
func evictPortHolders(ports []int) bool {
	evicted := false
	for _, p := range ports {
		for _, pid := range listenersOnPort(p) {
			if pid <= 1 {
				continue
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				continue
			}
			_ = proc.Signal(os.Interrupt)
			time.Sleep(200 * time.Millisecond)
			_ = proc.Kill()
			evicted = true
		}
	}
	return evicted
}

// listenersOnPort returns the PIDs of processes listening on the given local
// TCP port. It uses lsof on macOS and ss on Linux.
func listenersOnPort(port int) []int {
	var out []byte
	var err error
	if runtime.GOOS == "darwin" {
		out, err = exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	} else {
		// ss -tlnp output line: State Recv-Q Send-Q Local-Address:Port ... users:(("ssh",pid=N,...))
		out, err = exec.Command("ss", "-tlnp", "sport", "=", ":"+strconv.Itoa(port)).Output()
	}
	if err != nil || len(out) == 0 {
		return nil
	}
	seen := map[int]struct{}{}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, f := range fields[1:] {
			// lsof: second column is PID
			// ss: look for pid=N inside users:((...))
			if runtime.GOOS == "darwin" {
				if pid, e := strconv.Atoi(f); e == nil && pid > 1 {
					if _, dup := seen[pid]; !dup {
						seen[pid] = struct{}{}
						pids = append(pids, pid)
					}
					break
				}
			} else {
				if idx := strings.Index(f, "pid="); idx >= 0 {
					rest := f[idx+4:]
					end := strings.IndexAny(rest, ",)")
					if end < 0 {
						end = len(rest)
					}
					if pid, e := strconv.Atoi(rest[:end]); e == nil && pid > 1 {
						if _, dup := seen[pid]; !dup {
							seen[pid] = struct{}{}
							pids = append(pids, pid)
						}
					}
				}
			}
		}
	}
	return pids
}

func (m *MultiTunnel) buildArgs(fwds []Forward, resolved []int) []string {
	var args []string

	if m.identityFile != "" {
		args = append(args,
			"-F", "/dev/null",
			"-i", m.identityFile,
			"-o", "IdentitiesOnly=yes",
			"-o", "IdentityAgent=none",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
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

	args = append(args, "-N")
	for i, f := range fwds {
		rh := f.RemoteHost
		if rh == "" {
			rh = "127.0.0.1"
		}
		args = append(args, "-L",
			fmt.Sprintf("localhost:%d:%s:%d", resolved[i], rh, f.RemotePort),
		)
	}
	args = append(args,
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
	)
	if m.sshPort > 0 && m.sshPort != 22 {
		args = append([]string{"-p", strconv.Itoa(m.sshPort)}, args...)
	}
	args = append(args, m.host)
	return args
}

// waitReady waits until all local ports are bound and the SSH process is alive.
func (m *MultiTunnel) waitReady(ports []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Wait until all local listener ports are bound.
	// SSH binds local ports only after authentication and forwarding are set up
	// (ExitOnForwardFailure=yes means it exits if any forward fails to bind remotely).
	for time.Now().Before(deadline) {
		allBound := true
		for _, p := range ports {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", p), 100*time.Millisecond)
			if err != nil {
				allBound = false
				break
			}
			_ = conn.Close()
		}
		if allBound {
			// All ports bound and SSH process is still running — tunnel is ready.
			return nil
		}

		// Check if SSH exited early (auth failure, port conflict, etc.).
		if m.cmd.ProcessState != nil {
			return fmt.Errorf("ssh process exited (code %d) before tunnel was ready", m.cmd.ProcessState.ExitCode())
		}

		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("tunnel did not become ready within %s", timeout)
}
