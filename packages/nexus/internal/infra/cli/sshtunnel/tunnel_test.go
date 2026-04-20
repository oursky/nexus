package sshtunnel

import (
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
