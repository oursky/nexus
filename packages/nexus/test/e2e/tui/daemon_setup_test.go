//go:build e2e

package tui_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
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
	if moduleRoot == "" {
		panic("tui e2e: could not locate module root (go.mod)")
	}
}

type daemonEnv struct {
	BinPath    string
	WSURL      string
	Token      string
	SocketPath string
	cmd        *exec.Cmd
	dbDir      string
	sockDir    string
	workDir    string
}

func prepLinuxEmbed() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	gen := exec.Command("go", "generate", "./cmd/nexus/")
	gen.Dir = moduleRoot
	gen.Stdout = os.Stderr
	gen.Stderr = os.Stderr
	return gen.Run()
}

func buildNexusBinary() (string, error) {
	tmp := os.TempDir()
	bin := filepath.Join(tmp, fmt.Sprintf("nexus-tui-e2e-%d", os.Getpid()))
	build := exec.Command("go", "build", "-o", bin, "./cmd/nexus/")
	build.Dir = moduleRoot
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}
	return bin, nil
}

// resolveNexusBinary returns NEXUS_E2E_BINARY when set (CI supplies /tmp/nexus-bin
// from scripts/ci/build-nexus-libkrun.sh — same as test/e2e/harness). Otherwise
// builds a default nexus without libkrun embed (sandbox/local dev).
func resolveNexusBinary() (string, error) {
	if binPath := strings.TrimSpace(os.Getenv("NEXUS_E2E_BINARY")); binPath != "" {
		return binPath, nil
	}
	return buildNexusBinary()
}

func freeTCPPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// tuiDaemonDriverArgs selects sandbox vs VM-backed daemon flags.
//
// Linux VM (libkrun): set NEXUS_VM_KERNEL and NEXUS_VM_ROOTFS (or NEXUS_E2E_ROOTFS),
// matching packages/nexus/test/e2e/harness e2eVMArgs / CI linux e2e jobs.
//
// macOS VM: set NEXUS_E2E_DRIVER=vm and NEXUS_VM_ROOTFS (same as harness).
//
// Otherwise defaults to --driver sandbox (fast local runs without KVM).
func tuiDaemonDriverArgs() ([]string, error) {
	if runtime.GOOS == "darwin" && strings.EqualFold(strings.TrimSpace(os.Getenv("NEXUS_E2E_DRIVER")), "vm") {
		root := harness.VMRootfsFromEnv()
		if root == "" {
			return nil, fmt.Errorf("NEXUS_E2E_DRIVER=vm requires NEXUS_VM_ROOTFS or NEXUS_E2E_ROOTFS")
		}
		return []string{"--driver", "vm", "--rootfs", root}, nil
	}
	kernel := strings.TrimSpace(os.Getenv("NEXUS_VM_KERNEL"))
	if kernel != "" {
		if runtime.GOOS != "linux" {
			return nil, fmt.Errorf("NEXUS_VM_KERNEL is only supported on linux (GOOS=%s)", runtime.GOOS)
		}
		root := harness.VMRootfsFromEnv()
		if root == "" {
			return nil, fmt.Errorf("NEXUS_VM_KERNEL requires NEXUS_VM_ROOTFS or NEXUS_E2E_ROOTFS")
		}
		return []string{
			"--kernel", kernel,
			"--rootfs", root,
			"--driver", "libkrun",
		}, nil
	}
	return []string{"--driver", "sandbox"}, nil
}

func startDaemon(bin string) (*daemonEnv, error) {
	port, err := freeTCPPort()
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}

	driverArgs, err := tuiDaemonDriverArgs()
	if err != nil {
		return nil, err
	}

	dbDir, err := os.MkdirTemp("", "nexus-tui-e2e-db-*")
	if err != nil {
		return nil, err
	}
	sockDir, err := os.MkdirTemp("", "nexus-tui-e2e-sock-*")
	if err != nil {
		_ = os.RemoveAll(dbDir)
		return nil, err
	}
	workDir, err := os.MkdirTemp("", "nexus-tui-e2e-work-*")
	if err != nil {
		_ = os.RemoveAll(dbDir)
		_ = os.RemoveAll(sockDir)
		return nil, err
	}

	dbPath := filepath.Join(dbDir, "nexus.db")
	socketPath := filepath.Join(sockDir, "nexusd.sock")

	args := []string{
		"daemon", "start",
		"--db", dbPath,
		"--socket", socketPath,
		"--workdir-root", workDir,
	}
	args = append(args, driverArgs...)
	args = append(args,
		"--network=true",
		"--bind", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--token", token,
		"--foreground",
	)

	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dbDir)
		_ = os.RemoveAll(sockDir)
		_ = os.RemoveAll(workDir)
		return nil, err
	}

	waitDaemon := 60 * time.Second
	if harness.IsVMBackend() {
		waitDaemon = 120 * time.Second
	}
	deadline := time.Now().Add(waitDaemon)
	var client *harness.Client
	for time.Now().Before(deadline) {
		c, err := harness.Dial(socketPath)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if err := c.Call("node.info", nil, nil); err != nil {
			_ = c.Close()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		client = c
		break
	}
	if client == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(dbDir)
		_ = os.RemoveAll(sockDir)
		_ = os.RemoveAll(workDir)
		return nil, fmt.Errorf("daemon did not accept unix RPC within 60s")
	}
	_ = client.Close()

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
		_ = os.RemoveAll(dbDir)
		_ = os.RemoveAll(sockDir)
		_ = os.RemoveAll(workDir)
		return nil, fmt.Errorf("daemon /healthz did not become ready")
	}

	env := &daemonEnv{
		BinPath:    bin,
		WSURL:      wsURL,
		Token:      token,
		SocketPath: socketPath,
		cmd:        cmd,
		dbDir:      dbDir,
		sockDir:    sockDir,
		workDir:    workDir,
	}
	return env, nil
}

func (e *daemonEnv) CLIEnv() []string {
	return append(os.Environ(),
		"NEXUS_E2E_DAEMON_WEBSOCKET="+e.WSURL,
		"NEXUS_DAEMON_TOKEN="+e.Token,
	)
}

func (e *daemonEnv) Close() {
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			_ = e.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = e.cmd.Process.Kill()
			<-done
		}
	}
	_ = os.RemoveAll(e.dbDir)
	_ = os.RemoveAll(e.sockDir)
	_ = os.RemoveAll(e.workDir)
}

func drainBackground(r io.Reader, buf *[]byte, stop chan struct{}) {
	b := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := r.Read(b)
		if n > 0 {
			*buf = append(*buf, b[:n]...)
		}
		if err != nil {
			return
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inCSI := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inCSI = true
		case inCSI:
			if r >= '@' && r <= '~' {
				inCSI = false
			}
		default:
			if !inCSI {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
