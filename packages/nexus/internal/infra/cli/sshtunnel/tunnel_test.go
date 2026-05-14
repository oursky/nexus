package sshtunnel

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFindAvailablePort(t *testing.T) {
	port, err := findAvailablePort()
	if err != nil {
		t.Fatalf("findAvailablePort() error: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("findAvailablePort() returned invalid port %d", port)
	}
}

func TestNew(t *testing.T) {
	m := New("newman@linuxbox", 7777, 22)
	if m.host != "newman@linuxbox" {
		t.Errorf("host = %q, want %q", m.host, "newman@linuxbox")
	}
	if m.remotePort != 7777 {
		t.Errorf("remotePort = %d, want 7777", m.remotePort)
	}
	if m.sshPort != 22 {
		t.Errorf("sshPort = %d, want 22", m.sshPort)
	}
	if m.localPort != 0 {
		t.Errorf("localPort = %d, want 0 (unset)", m.localPort)
	}
	if m.running {
		t.Error("running should be false on new manager")
	}
}

func TestNewWithCustomSSHPort(t *testing.T) {
	m := New("user@host", 9000, 2222)
	if m.sshPort != 2222 {
		t.Errorf("sshPort = %d, want 2222", m.sshPort)
	}
}

func TestEnsureSSHNotAvailable(t *testing.T) {
	// Use an unreachable host so SSH fails quickly
	m := New("invalid-host-that-does-not-exist.local", 7777, 0)
	_, err := m.Ensure()
	if err == nil {
		t.Error("Ensure() should fail when SSH target is unreachable")
		_ = m.Close()
	}
}

func TestCloseOnNonRunningManager(t *testing.T) {
	m := New("newman@linuxbox", 7777, 22)
	if err := m.Close(); err != nil {
		t.Errorf("Close() on non-running manager returned error: %v", err)
	}
}

func TestIsRunningInitiallyFalse(t *testing.T) {
	m := New("newman@linuxbox", 7777, 22)
	if m.IsRunning() {
		t.Error("IsRunning() should be false on new manager")
	}
}

func TestLocalPortInitiallyZero(t *testing.T) {
	m := New("newman@linuxbox", 7777, 22)
	if m.LocalPort() != 0 {
		t.Errorf("LocalPort() = %d, want 0", m.LocalPort())
	}
}

// TestLocalProxy verifies that StartLocalProxy correctly forwards TCP
// connections from LocalPort to RemotePort on localhost.
// This is the core fix for the "port 3000 not found" bug:
// libkrun VM workspaces expose services via an ephemeral daemon proxy port
// (Forward.RemotePort), not the original service port (Forward.LocalPort).
// StartLocalProxy binds LocalPort and proxies to RemotePort so users can
// connect to "localhost:3000" instead of the ephemeral port.
func TestLocalProxy(t *testing.T) {
	// Spin up a simple echo server to act as the "daemon proxy".
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello from backend")
	}))
	defer backend.Close()

	// Extract the backend port.
	backendAddr := strings.TrimPrefix(backend.URL, "http://")
	_, portStr, _ := net.SplitHostPort(backendAddr)
	backendPort := 0
	fmt.Sscan(portStr, &backendPort)
	if backendPort == 0 {
		t.Fatalf("could not parse backend port from %s", backendAddr)
	}

	// Pick a free port to use as the "local" (user-facing) port.
	localPort, err := findAvailablePort()
	if err != nil {
		t.Fatalf("findAvailablePort: %v", err)
	}

	// Start the local proxy: localPort → backendPort.
	fwds := []Forward{
		{LocalPort: localPort, RemoteHost: "127.0.0.1", RemotePort: backendPort},
	}
	lp, err := StartLocalProxy(fwds)
	if err != nil {
		t.Fatalf("StartLocalProxy: %v", err)
	}
	defer lp.Close()

	// Connect through the proxy.
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", localPort))
	if err != nil {
		t.Fatalf("GET through local proxy: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "hello from backend") {
		t.Errorf("body = %q, want 'hello from backend'", string(body))
	}

	// Verify Close does not error.
	if err := lp.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
