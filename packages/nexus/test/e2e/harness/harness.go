//go:build e2e

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var moduleRoot string

func init() {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			moduleRoot = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

type Harness struct {
	t          *testing.T
	socketPath string
	client     *Client
	cmd        *exec.Cmd
}

type Option func(*config)

type config struct {
	vmKernel string
	vmRootfs string
	nodeName string
}

// WithVM configures the harness to start the daemon with a specific kernel and rootfs.
func WithVM(kernel, rootfs string) Option {
	return func(c *config) {
		c.vmKernel = kernel
		c.vmRootfs = rootfs
	}
}

func WithNodeName(name string) Option {
	return func(c *config) {
		c.nodeName = name
	}
}

func New(t *testing.T, opts ...Option) *Harness {
	t.Helper()

	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	RequireVM(t)

	dbPath := TempDB(t)
	socketPath := TempSocket(t)
	workdir := TempWorkdir(t)

	binPath := resolveBinary(t)

	dc := daemonConfig{
		dbPath:     dbPath,
		socketPath: socketPath,
		workdir:    workdir,
		vmKernel:   cfg.vmKernel,
		vmRootfs:   cfg.vmRootfs,
		nodeName:   cfg.nodeName,
	}
	args := buildDaemonArgs(dc)
	cmd, client := launchAndWait(binPath, socketPath, args)

	h := &Harness{
		t:          t,
		socketPath: socketPath,
		client:     client,
		cmd:        cmd,
	}

	t.Cleanup(func() {
		stopDaemon(cmd)
		_ = client.Close()
	})

	return h
}

func (h *Harness) Call(method string, params, out any) error {
	return h.client.Call(method, params, out)
}

func (h *Harness) MustCall(method string, params, out any) {
	h.t.Helper()
	if err := h.Call(method, params, out); err != nil {
		h.t.Fatalf("MustCall %s: %v", method, err)
	}
}

func (h *Harness) SocketPath() string {
	return h.socketPath
}

// NewClient dials a fresh independent connection to the daemon socket.
// Use this when multiple goroutines or tests need concurrent RPC access to
// the same daemon — each call gets its own net.Conn so calls are not
// serialized by the shared h.client mutex.
func (h *Harness) NewClient() (*Client, error) {
	return Dial(h.socketPath)
}

// MustNewClient is like NewClient but fails the test on error.
// The returned client is closed automatically via t.Cleanup.
func (h *Harness) MustNewClient(t *testing.T) *Client {
	t.Helper()
	c, err := h.NewClient()
	if err != nil {
		t.Fatalf("harness: dial new client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// ForTest returns a test-scoped view of the shared harness.
// It dials a fresh independent connection bound to t's lifetime,
// and sets h.t = t so MustCall / Fatal work correctly.
//
// Use this in Tier-1 tests that share the suite daemon:
//
//	h := suite.Harness().ForTest(t)
func (h *Harness) ForTest(t *testing.T) *Harness {
	t.Helper()
	c := h.MustNewClient(t)
	return &Harness{
		t:          t,
		socketPath: h.socketPath,
		client:     c,
		cmd:        nil, // lifecycle managed by Suite, not this test
	}
}
