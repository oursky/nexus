//go:build e2e

package spotlight_test

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// startEchoServer starts a TCP echo server on a random port, returning port and a stop function.
func startEchoServer(t *testing.T) (port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo server listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				buf := make([]byte, 512)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				_, _ = c.Write(buf[:n])
			}(conn)
		}
	}()
	stop = func() {
		_ = ln.Close()
		<-done
	}
	return port, stop
}

// createSpotlightWorkspace creates and starts a workspace for spotlight tests.
func createSpotlightWorkspace(t *testing.T, h *harness.Harness, repoPath, name string) string {
	t.Helper()
	var res struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": name},
	}, &res)
	wsID := res.Workspace.ID
	if wsID == "" {
		t.Fatalf("create %s: empty id", name)
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.stop", map[string]any{"id": wsID}, nil)
		_ = h.Call("workspace.remove", map[string]any{"id": wsID}, nil)
	})
	h.MustCall("workspace.start", map[string]any{"id": wsID}, nil)
	harness.WaitForWorkspaceReady(t, h, wsID)
	return wsID
}

// Spec: VM-005, VM-PROOF-002, SPOT-013, SPOT-014, SPOT-015, SPOT-016, SPOT-017, SPOT-018, SPOT-019, SPOT-020, SPOT-021, SPOT-022, SPOT-023
// TestSpotlight_TCPProxyTraffic verifies that spotlight actually proxies TCP traffic.
func TestSpotlight_TCPProxyTraffic(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "spotlight-tcp")
	wsID := createSpotlightWorkspace(t, h, repoPath, "spotlight-tcp-test")

	remotePort, stopServer := startEchoServer(t)
	defer stopServer()
	localPort := harness.FreePort(t)

	// Start a spotlight forward: localPort → remotePort (echo server).
	var addRes struct {
		Forward struct {
			ID         string `json:"id"`
			LocalPort  int    `json:"localPort"`
			RemotePort int    `json:"remotePort"`
		} `json:"forward"`
	}
	if err := h.Call("spotlight.start", map[string]any{
		"workspaceId": wsID,
		"spec": map[string]any{
			"localPort":  localPort,
			"remotePort": remotePort,
		},
	}, &addRes); err != nil {
		t.Fatalf("spotlight.start: %v", err)
	}
	if addRes.Forward.ID == "" {
		t.Fatal("spotlight.start: empty forward id")
	}
	if addRes.Forward.LocalPort != localPort {
		t.Errorf("localPort=%d, want %d", addRes.Forward.LocalPort, localPort)
	}
	if addRes.Forward.RemotePort != remotePort {
		t.Errorf("remotePort=%d, want %d", addRes.Forward.RemotePort, remotePort)
	}
	t.Cleanup(func() {
		_ = h.Call("spotlight.stop", map[string]any{"workspaceId": wsID}, nil)
	})

	// Allow the listener to bind.
	time.Sleep(50 * time.Millisecond)

	// Dial the forward's local port and send a message.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 3*time.Second)
	if err != nil {
		t.Fatalf("dial spotlight forward %d: %v", localPort, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	payload := "NEXUS_SPOTLIGHT_ECHO_TEST\n"
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatalf("write to spotlight forward: %v", err)
	}

	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read echo from spotlight forward: %v", err)
	}
	got := string(buf[:n])
	if !strings.Contains(got, "NEXUS_SPOTLIGHT_ECHO_TEST") {
		t.Errorf("echo response: expected NEXUS_SPOTLIGHT_ECHO_TEST, got %q", got)
	}
}

// Spec: SPOT-017, SPOT-018, SPOT-019, SPOT-020, SPOT-021, SPOT-022, SPOT-023, INV-016
// TestSpotlight_ListAndStop verifies spotlight.list shows active forwards and spotlight.stop removes them.
func TestSpotlight_ListAndStop(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "spotlight-list")
	wsID := createSpotlightWorkspace(t, h, repoPath, "spotlight-list-test")

	remotePort, stopServer := startEchoServer(t)
	defer stopServer()
	localPort := harness.FreePort(t)

	var addRes struct {
		Forward struct {
			ID string `json:"id"`
		} `json:"forward"`
	}
	if err := h.Call("spotlight.start", map[string]any{
		"workspaceId": wsID,
		"spec":        map[string]any{"localPort": localPort, "remotePort": remotePort},
	}, &addRes); err != nil {
		t.Fatalf("spotlight.start: %v", err)
	}
	fwdID := addRes.Forward.ID

	// Verify it appears in spotlight.list.
	var listRes struct {
		Forwards []struct {
			ID string `json:"id"`
		} `json:"forwards"`
	}
	h.MustCall("spotlight.list", map[string]any{"workspaceId": wsID}, &listRes)
	found := false
	for _, f := range listRes.Forwards {
		if f.ID == fwdID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("spotlight.list: forward %s not found", fwdID)
	}

	// Stop all forwards for the workspace.
	var stopRes struct {
		Closed bool `json:"closed"`
	}
	h.MustCall("spotlight.stop", map[string]any{"workspaceId": wsID}, &stopRes)
	if !stopRes.Closed {
		t.Fatal("spotlight.stop: expected closed=true")
	}

	// Verify it no longer appears.
	h.MustCall("spotlight.list", map[string]any{"workspaceId": wsID}, &listRes)
	for _, f := range listRes.Forwards {
		if f.ID == fwdID {
			t.Fatalf("spotlight.list: forward %s still present after stop", fwdID)
		}
	}
}

// Spec: ERR-051
// TestSpotlight_PortConflict verifies that binding the same local port twice returns an error.
func TestSpotlight_PortConflict(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "spotlight-conflict")
	wsID := createSpotlightWorkspace(t, h, repoPath, "spotlight-conflict-test")

	remotePort, stopServer := startEchoServer(t)
	defer stopServer()
	localPort := harness.FreePort(t)

	var addRes struct {
		Forward struct {
			ID string `json:"id"`
		} `json:"forward"`
	}
	if err := h.Call("spotlight.start", map[string]any{
		"workspaceId": wsID,
		"spec":        map[string]any{"localPort": localPort, "remotePort": remotePort},
	}, &addRes); err != nil {
		t.Fatalf("spotlight.start first: %v", err)
	}
	t.Cleanup(func() {
		_ = h.Call("spotlight.stop", map[string]any{"workspaceId": wsID}, nil)
	})

	// Second bind on same local port should fail.
	err := h.Call("spotlight.start", map[string]any{
		"workspaceId": wsID,
		"spec":        map[string]any{"localPort": localPort, "remotePort": remotePort + 1},
	}, nil)
	if err == nil {
		t.Error("spotlight.start: expected error on port conflict, got nil")
	}
}

// Spec: SPOT-010, SPOT-011, SPOT-012
// TestSpotlight_WorkspacePortsAlias verifies workspace.ports.* are aliases for spotlight.*.
func TestSpotlight_WorkspacePortsAlias(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "spotlight-alias")
	wsID := createSpotlightWorkspace(t, h, repoPath, "spotlight-alias-test")

	remotePort, stopServer := startEchoServer(t)
	defer stopServer()
	localPort := harness.FreePort(t)

	// Add via workspace.ports.add.
	var addRes struct {
		Forward struct {
			ID string `json:"id"`
		} `json:"forward"`
	}
	if err := h.Call("workspace.ports.add", map[string]any{
		"workspaceId": wsID,
		"spec":        map[string]any{"localPort": localPort, "remotePort": remotePort},
	}, &addRes); err != nil {
		t.Fatalf("workspace.ports.add: %v", err)
	}
	fwdID := addRes.Forward.ID
	if fwdID == "" {
		t.Fatal("workspace.ports.add: empty forward id")
	}

	// List via workspace.ports.list.
	var listRes struct {
		Forwards []struct {
			ID string `json:"id"`
		} `json:"forwards"`
	}
	h.MustCall("workspace.ports.list", map[string]any{"workspaceId": wsID}, &listRes)
	found := false
	for _, f := range listRes.Forwards {
		if f.ID == fwdID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("workspace.ports.list: forward %s not found", fwdID)
	}

	// Remove via workspace.ports.remove.
	var removeRes struct {
		Closed bool `json:"closed"`
	}
	h.MustCall("workspace.ports.remove", map[string]any{
		"workspaceId": wsID,
		"forwardId":   fwdID,
	}, &removeRes)
	if !removeRes.Closed {
		t.Fatal("workspace.ports.remove: expected closed=true")
	}
}
