//go:build e2e

package harness

import (
	"fmt"
	"os"
	"testing"
)

// Suite manages a single shared daemon process for an entire test package.
// It is intended to be used from TestMain: start once, run all tests, stop once.
//
// Usage in a test package's main_test.go:
//
//	var suite *harness.Suite
//
//	func TestMain(m *testing.M) {
//	    suite = harness.NewSuite()
//	    os.Exit(suite.Run(m))
//	}
//
// Individual tests obtain a per-test client via suite.Harness().MustNewClient(t)
// instead of calling harness.New(t), which would spin up a new daemon per test.
type Suite struct {
	harness *Harness
}

// NewSuite runs the preflight check and, if the host supports VM-backed tests,
// starts a shared daemon process. Call suite.Run(m) from TestMain.
//
// On unsupported hosts (e.g. macOS, no /dev/kvm) Run() returns 0 immediately
// so the package exits successfully without running any tests. Individual per-test
// RequireVM calls would each skip, but by exiting early from TestMain we avoid
// the overhead of spawning the daemon and the confusion of 100 skip messages.
func NewSuite() *Suite {
	r := RunPreflight(RealEnvReader{})
	switch r.Status {
	case PreflightReady:
		// fall through to start daemon
	case PreflightUnsupportedHost:
		// Return a nil-harness suite; Run() will exit 0.
		return &Suite{}
	default:
		// MISCONFIGURED or BOOTSTRAP_FAILED — print diagnostics and exit 1.
		PrintPreflightReport(os.Stderr, r)
		os.Exit(1)
	}

	h := startSharedDaemon()
	return &Suite{harness: h}
}

// Run invokes m.Run() and cleans up the shared daemon afterwards.
// Call os.Exit(suite.Run(m)) from TestMain.
func (s *Suite) Run(m *testing.M) int {
	if s.harness == nil {
		_, _ = fmt.Fprintln(os.Stderr, "suite: VM backend not available on this host — skipping all tests in this package")
		return 0
	}
	defer s.teardown()
	return m.Run()
}

// Harness returns the shared *Harness. Tests should obtain an independent client
// via h.MustNewClient(t) for each test to avoid sharing the same serialized connection.
func (s *Suite) Harness() *Harness {
	return s.harness
}

// teardown stops the shared daemon gracefully.
func (s *Suite) teardown() {
	if s.harness == nil || s.harness.cmd == nil {
		return
	}
	stopDaemon(s.harness.cmd)
	if s.harness.client != nil {
		_ = s.harness.client.Close()
	}
}

// startSharedDaemon starts a daemon process not tied to any *testing.T.
// Lifecycle is managed by Suite.teardown(), not t.Cleanup.
func startSharedDaemon() *Harness {
	dbDir, err := os.MkdirTemp("", "nexus-e2e-suite-db-*")
	if err != nil {
		panic("suite: mktemp db: " + err.Error())
	}
	sockDir, err := os.MkdirTemp("", "nexus-e2e-suite-sock-*")
	if err != nil {
		panic("suite: mktemp sock: " + err.Error())
	}

	workdirBase := ""
	if _, statErr := os.Stat("/data/nexus"); statErr == nil {
		workdirBase = "/data/nexus/libkrun-vms-e2e"
		_ = os.MkdirAll(workdirBase, 0o755)
	}
	workdir, err := os.MkdirTemp(workdirBase, "nexus-e2e-suite-workdir-*")
	if err != nil {
		panic("suite: mktemp workdir: " + err.Error())
	}

	dbPath := dbDir + "/nexus.db"
	socketPath := sockDir + "/nexusd.sock"

	binPath, binCleanup := resolveBinaryNoTest()

	dc := daemonConfig{
		dbPath:     dbPath,
		socketPath: socketPath,
		workdir:    workdir,
		vmKernel:   os.Getenv("NEXUS_VM_KERNEL"),
		vmRootfs:   os.Getenv("NEXUS_VM_ROOTFS"),
	}
	args := buildDaemonArgs(dc)
	cmd, client := launchAndWait(binPath, socketPath, args)

	// Clean up temp dirs when the process exits. We cannot use t.Cleanup here.
	go func() {
		_ = cmd.Wait()
		_ = os.RemoveAll(dbDir)
		_ = os.RemoveAll(sockDir)
		_ = os.RemoveAll(workdir)
		binCleanup()
	}()

	// Return a Harness with nil t — callers must use MustNewClient(t) per test.
	// Direct h.Call / h.MustCall are available but serialize all callers on h.client.
	return &Harness{
		t:          nil, // no test context; Suite manages lifecycle
		socketPath: socketPath,
		client:     client,
		cmd:        cmd,
	}
}

// MustNewClient creates a fresh per-test RPC connection to the suite's shared daemon.
// This is the preferred way for test functions to access the daemon when using a Suite,
// because it avoids serializing all tests on the Suite's single shared client connection.
//
// The connection is automatically closed when t finishes.
func (s *Suite) MustNewClient(t *testing.T) *Client {
	t.Helper()
	return s.harness.MustNewClient(t)
}
