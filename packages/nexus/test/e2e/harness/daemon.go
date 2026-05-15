//go:build e2e

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// resolveBinary returns the path to the nexusd binary.
// It uses NEXUS_E2E_BINARY if set, otherwise builds from source.
// When building from source, the caller must supply a cleanup hook.
func resolveBinary(t *testing.T) string {
	t.Helper()
	ensureDarwinLibkrunDylibs()
	binPath := os.Getenv("NEXUS_E2E_BINARY")
	if binPath != "" {
		return binPath
	}
	tmp, err := os.MkdirTemp("", "nexus-e2e-bin-*")
	if err != nil {
		t.Fatalf("harness: mktemp for binary: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })

	binPath = filepath.Join(tmp, "nexusd")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/nexus/")
	build.Dir = moduleRoot
	build.Stderr = os.Stderr
	if out, err := build.Output(); err != nil {
		t.Fatalf("harness: build nexusd: %v\n%s", err, out)
	}
	if err := adhocSignNexusForHypervisor(binPath); err != nil {
		t.Fatalf("harness: codesign nexusd: %v", err)
	}
	return binPath
}

// resolveBinaryNoTest returns the nexusd binary path without a *testing.T.
// If NEXUS_E2E_BINARY is not set it builds from source into a temp directory
// and returns both the path and a cleanup function.
func resolveBinaryNoTest() (binPath string, cleanup func()) {
	ensureDarwinLibkrunDylibs()
	binPath = os.Getenv("NEXUS_E2E_BINARY")
	if binPath != "" {
		return binPath, func() {}
	}
	tmp, err := os.MkdirTemp("", "nexus-e2e-bin-*")
	if err != nil {
		panic("harness: mktemp for binary: " + err.Error())
	}
	binPath = filepath.Join(tmp, "nexusd")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/nexus/")
	build.Dir = moduleRoot
	build.Stderr = os.Stderr
	if out, err := build.Output(); err != nil {
		_ = os.RemoveAll(tmp)
		panic("harness: build nexusd: " + err.Error() + "\n" + string(out))
	}
	if err := adhocSignNexusForHypervisor(binPath); err != nil {
		_ = os.RemoveAll(tmp)
		panic("harness: codesign nexusd: " + err.Error())
	}
	return binPath, func() { os.RemoveAll(tmp) }
}

func repoRoot() string {
	return filepath.Clean(filepath.Join(moduleRoot, "..", ".."))
}

func ensureDarwinLibkrunDylibs() {
	if runtime.GOOS != "darwin" {
		return
	}
	lib := filepath.Join(moduleRoot, "cmd", "nexus", "libkrun-darwin-arm64.dylib")
	if st, err := os.Stat(lib); err == nil && st.Size() > 0 {
		return
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot(), "scripts", "local", "build-libkrun-darwin.sh"))
	cmd.Dir = repoRoot()
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("harness: build-libkrun-darwin.sh: " + err.Error())
	}
}

// e2eVMArgs returns extra daemon start flags for VM-backed E2E (Linux libkrun or macOS vm driver).
func e2eVMArgs() []string {
	if runtime.GOOS == "darwin" && strings.EqualFold(strings.TrimSpace(os.Getenv("NEXUS_E2E_DRIVER")), "vm") {
		root := VMRootfsFromEnv()
		var out []string
		if root != "" {
			out = append(out, "--rootfs", root)
		}
		return out
	}
	kernel := strings.TrimSpace(os.Getenv("NEXUS_VM_KERNEL"))
	if kernel != "" {
		return []string{
			"--kernel", kernel,
			"--rootfs", VMRootfsFromEnv(),
		}
	}
	return nil
}

type daemonConfig struct {
	dbPath     string
	socketPath string
	workdir    string
	vmKernel   string
	vmRootfs   string
	nodeName   string
}

// buildDaemonArgs returns the CLI args for starting nexusd with the given config.
func buildDaemonArgs(cfg daemonConfig) []string {
	args := []string{
		"daemon", "start",
		"--db", cfg.dbPath,
		"--socket", cfg.socketPath,
		"--workdir-root", cfg.workdir,
		"--network=false",
		"--foreground",
	}
	if extra := e2eVMArgs(); len(extra) > 0 {
		args = append(args, extra...)
	} else if cfg.vmKernel != "" {
		args = append(args,
			"--kernel", cfg.vmKernel,
			"--rootfs", cfg.vmRootfs,
		)
	}
	if cfg.nodeName != "" {
		args = append(args, "--node-name", cfg.nodeName)
	}
	return args
}

// launchAndWait starts nexusd and waits up to 120 s for it to accept RPC.
// Returns the running cmd and a connected client. Panics on failure.
func launchAndWait(binPath, socketPath string, args []string) (*exec.Cmd, *Client) {
	cmd := exec.Command(binPath, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		panic("harness: start nexusd: " + err.Error())
	}

	deadline := time.Now().Add(120 * time.Second)
	var client *Client
	for time.Now().Before(deadline) {
		c, err := Dial(socketPath)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if err := c.Call("node.info", nil, nil); err != nil {
			c.Close()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		client = c
		break
	}
	if client == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		panic("harness: nexusd did not become ready within 120s")
	}
	return cmd, client
}

// stopDaemon sends SIGTERM to cmd and waits, escalating to SIGKILL after 5 s.
func stopDaemon(cmd *exec.Cmd) {
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
}
