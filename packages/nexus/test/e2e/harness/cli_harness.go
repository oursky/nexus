//go:build e2e

package harness

import (
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// CLIHarness runs the nexus daemon with a loopback WebSocket listener so subprocess
// CLI invocations can use EnsureDaemon via NEXUS_E2E_DAEMON_WEBSOCKET + NEXUS_DAEMON_TOKEN.
// Unix JSON-RPC is still available on the same daemon for test setup (workspace.create, etc.).
type CLIHarness struct {
	*Harness
	wsURL      string
	token      string
	binPath    string
	daemonPort int
	configHome string // lazily filled when using remote profile mode
}

// NewCLIHarness starts a foreground daemon with --network on a free port.
func NewCLIHarness(t *testing.T) *CLIHarness {
	t.Helper()

	// Check Firecracker availability before building the binary so that skips
	// happen fast (no multi-second compile) on unsupported platforms / CI
	// environments that lack Firecracker configuration.
	RequireFirecracker(t)

	dbPath := TempDB(t)
	socketPath := TempSocket(t)
	workdir := TempWorkdir(t)

	port, err := freeLocalPort()
	if err != nil {
		t.Fatalf("cliharness: free port: %v", err)
	}
	token := randomToken(t)

	binPath := os.Getenv("NEXUS_E2E_BINARY")
	if binPath == "" {
		tmp, err := os.MkdirTemp("", "nexus-e2e-bin-*")
		if err != nil {
			t.Fatalf("cliharness: mktemp for binary: %v", err)
		}
		t.Cleanup(func() { os.RemoveAll(tmp) })

		binPath = filepath.Join(tmp, "nexusd")
		build := exec.Command("go", "build", "-o", binPath, "./cmd/nexus/")
		build.Dir = moduleRoot
		build.Stderr = os.Stderr
		if out, err := build.Output(); err != nil {
			t.Fatalf("cliharness: build nexus: %v\n%s", err, out)
		}
	}

	args := []string{
		"daemon", "start",
		"--db", dbPath,
		"--socket", socketPath,
		"--workdir-root", workdir,
		"--network=true",
		"--bind", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--token", token,
		"--foreground", // skip self-daemonize; harness kills the process directly
	}

	cmd := exec.Command(binPath, args...)
	cmd.Stdout = os.Stderr // capture startup messages
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("cliharness: start daemon: %v", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	var client *Client
	for time.Now().Before(deadline) {
		c, err := Dial(socketPath)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if err := c.Call("node.info", nil, nil); err != nil {
			c.Close()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		client = c
		break
	}
	if client == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("cliharness: daemon did not accept unix RPC within 15s")
	}

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/", port)
	healthOK := false
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthOK = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthOK {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("cliharness: network listener /healthz did not become ready within 15s")
	}

	h := &Harness{
		t:          t,
		socketPath: socketPath,
		client:     client,
		cmd:        cmd,
	}

	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = client.Close()
	})

	return &CLIHarness{
		Harness:    h,
		wsURL:      wsURL,
		token:      token,
		binPath:    binPath,
		daemonPort: port,
	}
}

// Run executes the nexus CLI with args, with cwd dir and env wired for this harness.
func (c *CLIHarness) Run(t *testing.T, dir string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(c.binPath, args...)
	cmd.Dir = dir
	cmd.Env = c.cliEnv(c.configHomeFor(t))
	return cmd.CombinedOutput()
}

// BinPath returns the nexus binary used for this harness.
func (c *CLIHarness) BinPath() string { return c.binPath }

// DaemonPort returns the TCP port the daemon's network listener is bound to.
func (c *CLIHarness) DaemonPort() int { return c.daemonPort }

// WebSocketURL is the ws:// URL subprocesses should use with NEXUS_E2E_DAEMON_WEBSOCKET.
func (c *CLIHarness) WebSocketURL() string { return c.wsURL }

// DaemonToken is the bearer token for NEXUS_DAEMON_TOKEN when dialing WebSocketURL.
func (c *CLIHarness) DaemonToken() string { return c.token }

// RunWithStdin runs the CLI with stdin set (e.g. non-interactive workspace shell).
func (c *CLIHarness) RunWithStdin(t *testing.T, dir, stdin string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(c.binPath, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = c.cliEnv(c.configHomeFor(t))
	return cmd.CombinedOutput()
}

func (c *CLIHarness) configHomeFor(t *testing.T) string {
	t.Helper()
	if !E2EUseRemoteProfile() {
		return ""
	}
	return c.ConfigHomeForCLI(t)
}

// SubprocessEnv is the full environment for an external nexus subprocess (e.g. PTY-driven shell).
func (c *CLIHarness) SubprocessEnv(t *testing.T) []string {
	t.Helper()
	return c.cliEnv(c.configHomeFor(t))
}

func freeLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

func randomToken(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		t.Fatalf("cliharness: token: %v", err)
	}
	return fmt.Sprintf("%x", b)
}
