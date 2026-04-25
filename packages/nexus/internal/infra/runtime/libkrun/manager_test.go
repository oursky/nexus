//go:build linux

package libkrun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsLikelyWorkspaceVMCmdline(t *testing.T) {
	wsID := "ws-123"
	ok := isLikelyWorkspaceVMCmdline("/home/u/.local/share/nexus/bin/nexus-libkrun-vm --config=/data/nexus/libkrun-vms/ws-123/libkrun-config.json", wsID)
	if !ok {
		t.Fatal("expected libkrun cmdline to match workspace vm")
	}
	ok = isLikelyWorkspaceVMCmdline("/home/u/.local/bin/other-daemon --config=/tmp/x", wsID)
	if ok {
		t.Fatal("expected non-libkrun cmdline to be rejected")
	}
}

func TestIsLikelyPasstCmdline(t *testing.T) {
	if !isLikelyPasstCmdline("passt --fd 3 --foreground --stderr") {
		t.Fatal("expected passt cmdline to match")
	}
	if isLikelyPasstCmdline("/usr/bin/python3 -m http.server") {
		t.Fatal("expected non-passt cmdline to be rejected")
	}
}

func TestReadPIDFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pid")
	if err := os.WriteFile(p, []byte("1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pid, err := readPIDFile(p)
	if err != nil {
		t.Fatalf("readPIDFile returned error: %v", err)
	}
	if pid != 1234 {
		t.Fatalf("expected pid 1234, got %d", pid)
	}
}
