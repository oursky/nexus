//go:build e2e

package harness

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

// CLISuite manages a single shared daemon process (with --network enabled) for an
// entire test package that needs CLI subprocess testing.
//
// Unlike Suite (which shares the daemon for direct RPC tests), CLISuite also binds
// a loopback WebSocket listener so subprocess nexus CLI invocations can authenticate
// via NEXUS_E2E_DAEMON_WEBSOCKET + NEXUS_DAEMON_TOKEN.
//
// All tests in the package share the same WS URL and token — test isolation is
// achieved by using unique workspace names/IDs, not unique tokens or ports.
//
// Usage in a package's main_test.go:
//
//	var cliSuite *harness.CLISuite
//
//	func TestMain(m *testing.M) {
//	    cliSuite = harness.NewCLISuite()
//	    os.Exit(cliSuite.Run(m))
//	}
//
// Individual tests call cliSuite.NewCLIHarness(t) to get a per-test CLIHarness
// that shares the daemon but gets a fresh per-test configHome.
type CLISuite struct {
	harness    *Harness
	wsURL      string
	token      string
	binPath    string
	daemonPort int
}

// NewCLISuite runs preflight, and if the host supports VM-backed tests, starts a
// shared daemon with a loopback WebSocket listener on a free port.
func NewCLISuite() *CLISuite {
	r := RunPreflight(RealEnvReader{})
	switch r.Status {
	case PreflightReady:
		// fall through to start daemon
	case PreflightUnsupportedHost:
		return &CLISuite{}
	default:
		PrintPreflightReport(os.Stderr, r)
		os.Exit(1)
	}

	port, err := freeSuitePort()
	if err != nil {
		panic("cli_suite: free port: " + err.Error())
	}
	token, err := randomSuiteToken()
	if err != nil {
		panic("cli_suite: token: " + err.Error())
	}

	binPath, binCleanup := resolveBinaryNoTest()

	dbDir, err := os.MkdirTemp("", "nexus-e2e-clisuite-db-*")
	if err != nil {
		panic("cli_suite: mktemp db: " + err.Error())
	}
	sockDir, err := os.MkdirTemp("", "nexus-e2e-clisuite-sock-*")
	if err != nil {
		panic("cli_suite: mktemp sock: " + err.Error())
	}
	workdirBase := ""
	if _, statErr := os.Stat("/data/nexus"); statErr == nil {
		workdirBase = "/data/nexus/libkrun-vms-e2e"
		_ = os.MkdirAll(workdirBase, 0o755)
	}
	workdir, err := os.MkdirTemp(workdirBase, "nexus-e2e-clisuite-workdir-*")
	if err != nil {
		panic("cli_suite: mktemp workdir: " + err.Error())
	}

	dbPath := dbDir + "/nexus.db"
	socketPath := sockDir + "/nexusd.sock"

	dc := daemonConfig{
		dbPath:     dbPath,
		socketPath: socketPath,
		workdir:    workdir,
		vmKernel:   os.Getenv("NEXUS_VM_KERNEL"),
		vmRootfs:   os.Getenv("NEXUS_VM_ROOTFS"),
	}
	args := buildDaemonArgs(dc)
	// Add network listener flags.
	args = append(args,
		"--network=true",
		"--bind", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--token", token,
	)

	cmd, client := launchAndWait(binPath, socketPath, args)

	// Wait for the HTTP /healthz endpoint to be ready.
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/", port)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	go func() {
		_ = cmd.Wait()
		_ = os.RemoveAll(dbDir)
		_ = os.RemoveAll(sockDir)
		_ = os.RemoveAll(workdir)
		binCleanup()
	}()

	h := &Harness{
		t:          nil,
		socketPath: socketPath,
		client:     client,
		cmd:        cmd,
	}

	return &CLISuite{
		harness:    h,
		wsURL:      wsURL,
		token:      token,
		binPath:    binPath,
		daemonPort: port,
	}
}

// Run invokes m.Run() and cleans up the shared daemon afterwards.
// Call os.Exit(cliSuite.Run(m)) from TestMain.
func (s *CLISuite) Run(m *testing.M) int {
	if s.harness == nil {
		_, _ = fmt.Fprintln(os.Stderr, "cli_suite: VM backend not available on this host — skipping all tests in this package")
		return 0
	}
	defer s.teardown()
	return m.Run()
}

// teardown stops the shared daemon gracefully.
func (s *CLISuite) teardown() {
	if s.harness == nil || s.harness.cmd == nil {
		return
	}
	stopDaemon(s.harness.cmd)
	if s.harness.client != nil {
		_ = s.harness.client.Close()
	}
}

// NewCLIHarness returns a CLIHarness backed by the suite's shared daemon.
// The returned CLIHarness uses the same WS URL and token as all other tests
// in this package — isolation is via unique workspace names/IDs, not credentials.
//
// A per-test configHome is set up if E2EUseRemoteProfile() is true.
func (s *CLISuite) NewCLIHarness(t *testing.T) *CLIHarness {
	t.Helper()
	if s.harness == nil {
		t.Skip("VM backend not available on this host")
	}
	RequireVM(t)

	// Use the per-test client from the shared harness so the caller gets an
	// independent connection (avoids serializing all tests on one socket).
	h := &Harness{
		t:          t,
		socketPath: s.harness.socketPath,
		client:     s.harness.MustNewClient(t),
		cmd:        nil, // lifecycle is managed by CLISuite, not t.Cleanup
	}

	cli := &CLIHarness{
		Harness:    h,
		wsURL:      s.wsURL,
		token:      s.token,
		binPath:    s.binPath,
		daemonPort: s.daemonPort,
	}
	if E2EUseRemoteProfile() {
		_ = cli.ConfigHomeForCLI(t)
	}
	return cli
}

// WebSocketURL is the ws:// URL tests should pass to CLI subprocesses.
func (s *CLISuite) WebSocketURL() string { return s.wsURL }

// DaemonToken is the bearer token for NEXUS_DAEMON_TOKEN.
func (s *CLISuite) DaemonToken() string { return s.token }

// DaemonPort is the TCP port the network listener is bound to.
func (s *CLISuite) DaemonPort() int { return s.daemonPort }

// freeSuitePort returns an available TCP port for the suite daemon.
func freeSuitePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// randomSuiteToken generates a cryptographically random bearer token for the suite.
func randomSuiteToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
