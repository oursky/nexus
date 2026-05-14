//go:build e2e

package pty_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/ptyhost"
)

// TestPTYHost_Process tests the PTY host subprocess (same binary as nexus: __pty-host).
func TestPTYHost_Process(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "pty-host.sock")

	binPath := filepath.Join(tmpDir, "nexus-pty-test")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/nexus")
	buildCmd.Dir = "../../.." // packages/nexus
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build nexus: %v\n%s", err, out)
	}

	cmd := exec.Command(binPath, ptyhost.HiddenSubcommand, "--socket", socketPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pty-host: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Wait for socket
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Connect client
	client := ptyhost.NewClient(socketPath)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect to pty-host: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Test create
	createParams, _ := json.Marshal(map[string]any{
		"workspaceId": "test-ws",
		"name":        "test-session",
		"cols":        80,
		"rows":        24,
	})
	result, err := client.Create(ctx, createParams)
	if err != nil {
		t.Fatalf("pty.create: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("pty.create: expected map result, got %T", result)
	}
	sessionID, _ := resultMap["id"].(string)
	if sessionID == "" {
		t.Fatal("pty.create: got empty session id")
	}

	// Test list
	listParams, _ := json.Marshal(map[string]any{
		"workspaceId": "test-ws",
	})
	result, err = client.List(ctx, listParams)
	if err != nil {
		t.Fatalf("pty.list: %v", err)
	}
	listMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("pty.list: expected map result, got %T", result)
	}
	sessions, _ := listMap["sessions"].([]any)
	found := false
	for _, s := range sessions {
		sess, _ := s.(map[string]any)
		if sess["id"] == sessionID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pty.list: session %s not found", sessionID)
	}

	// Test write
	writeParams, _ := json.Marshal(map[string]any{
		"sessionId": sessionID,
		"data":      "echo hello\n",
	})
	_, err = client.Write(ctx, writeParams)
	if err != nil {
		t.Fatalf("pty.write: %v", err)
	}

	// Test close
	closeParams, _ := json.Marshal(map[string]any{
		"sessionId": sessionID,
	})
	_, err = client.CloseSession(ctx, closeParams)
	if err != nil {
		t.Fatalf("pty.close: %v", err)
	}
}
