//go:build e2e

package vmproof_test

import (
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: VM-025
// TestVMProof_SSHAgentProxy_SocketExists verifies that the SSH agent proxy
// socket exists inside the guest VM and that SSH_AUTH_SOCK points to it.
func TestVMProof_SSHAgentProxy_SocketExists(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "vmproof-ssh-agent-socket")
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-ssh-agent-socket")

	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"test -S /tmp/ssh-agent.sock && echo OK; sleep 0.05")
	if err != nil {
		t.Fatalf("socket check: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "OK") {
		t.Errorf("expected /tmp/ssh-agent.sock to exist as a socket, got %q", string(out))
	}

	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"echo $SSH_AUTH_SOCK; sleep 0.05")
	if err != nil {
		t.Fatalf("SSH_AUTH_SOCK check: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "/tmp/ssh-agent.sock") {
		t.Errorf("expected SSH_AUTH_SOCK=/tmp/ssh-agent.sock, got %q", string(out))
	}
}

// Spec: VM-025b
// TestVMProof_SSHAgentProxy_ProxyLiveness verifies the SSH agent proxy is alive
// and responds to requests — ssh-add -l succeeds or reports no identities, but
// never fails with a connection error.
func TestVMProof_SSHAgentProxy_ProxyLiveness(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "vmproof-ssh-agent-liveness")
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-ssh-agent-liveness")

	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"ssh-add -l 2>&1 || true; sleep 0.05")
	if err != nil {
		t.Fatalf("ssh-add -l: %v\noutput: %s", err, out)
	}
	outStr := string(out)
	if strings.Contains(outStr, "Could not open") {
		t.Errorf("SSH agent proxy not reachable: %q", outStr)
	}
	if strings.Contains(outStr, "connect") {
		t.Errorf("SSH agent proxy connection error: %q", outStr)
	}
}

// Spec: VM-022b
// TestVMProof_SSHAgentProxy_LifecycleRobustness verifies that the SSH agent
// proxy socket is re-created after a workspace stop/start cycle.
func TestVMProof_SSHAgentProxy_LifecycleRobustness(t *testing.T) {
	harness.SkipIfVMBoot(t)
	t.Parallel()
	h := cliSuite.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "vmproof-ssh-agent-lifecycle")
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-ssh-agent-lifecycle")

	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"test -S /tmp/ssh-agent.sock && echo OK; sleep 0.05")
	if err != nil {
		t.Fatalf("socket check before stop: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "OK") {
		t.Errorf("before stop: expected /tmp/ssh-agent.sock to exist as a socket, got %q", string(out))
	}

	h.MustCall("workspace.stop", map[string]any{"id": wsID}, nil)

	h.MustCall("workspace.start", map[string]any{"id": wsID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, wsID)

	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"test -S /tmp/ssh-agent.sock && echo OK; sleep 0.05")
	if err != nil {
		t.Fatalf("socket check after restart: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "OK") {
		t.Errorf("after restart: expected /tmp/ssh-agent.sock to exist as a socket, got %q", string(out))
	}

	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"echo $SSH_AUTH_SOCK; sleep 0.05")
	if err != nil {
		t.Fatalf("SSH_AUTH_SOCK check after restart: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "/tmp/ssh-agent.sock") {
		t.Errorf("after restart: expected SSH_AUTH_SOCK=/tmp/ssh-agent.sock, got %q", string(out))
	}

	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"ssh-add -l 2>&1 || true; sleep 0.05")
	if err != nil {
		t.Fatalf("ssh-add -l after restart: %v\noutput: %s", err, out)
	}
	outStr := string(out)
	if strings.Contains(outStr, "Could not open") {
		t.Errorf("after restart: SSH agent proxy not reachable: %q", outStr)
	}
	if strings.Contains(outStr, "connect") {
		t.Errorf("after restart: SSH agent proxy connection error: %q", outStr)
	}
}

// Spec: VM-026
// TestVMProof_SSHAgentProxy_ForkIsolation verifies that both parent and child
// workspaces each have their own /tmp/ssh-agent.sock after a fork.
func TestVMProof_SSHAgentProxy_ForkIsolation(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)

	repoPath := harness.MakeLocalGitRepo(t, "vmproof-ssh-agent-fork")

	var parentRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "vmproof-ssh-agent-fork-parent"},
	}, &parentRes)
	parentID := parentRes.Workspace.ID
	if parentID == "" {
		t.Fatal("create parent: empty id")
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": parentID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": parentID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, parentID)

	var forkRes struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id": parentID, "childWorkspaceName": "vmproof-ssh-agent-fork-child", "childRef": "main",
	}, &forkRes)
	if !forkRes.Forked {
		t.Fatal("fork: expected forked=true")
	}
	childID := forkRes.Workspace.ID
	if childID == "" {
		t.Fatal("fork: empty child id")
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": childID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": childID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, childID)
	// After fork, CheckpointFork stops+restarts the parent VM asynchronously.
	// Wait for the parent to be ready again before exec-ing into it.
	harness.WaitForWorkspaceReady(t, h.Harness, parentID)

	parentSock, err := h.Run(t, repoPath, "workspace", "exec", parentID, "--", "sh", "-c",
		"echo $SSH_AUTH_SOCK; sleep 0.05")
	if err != nil {
		t.Fatalf("parent SSH_AUTH_SOCK: %v\noutput: %s", err, parentSock)
	}
	if !strings.Contains(string(parentSock), "/tmp/ssh-agent.sock") {
		t.Errorf("parent: expected SSH_AUTH_SOCK=/tmp/ssh-agent.sock, got %q", string(parentSock))
	}

	childSock, err := h.Run(t, repoPath, "workspace", "exec", childID, "--", "sh", "-c",
		"echo $SSH_AUTH_SOCK; sleep 0.05")
	if err != nil {
		t.Fatalf("child SSH_AUTH_SOCK: %v\noutput: %s", err, childSock)
	}
	if !strings.Contains(string(childSock), "/tmp/ssh-agent.sock") {
		t.Errorf("child: expected SSH_AUTH_SOCK=/tmp/ssh-agent.sock, got %q", string(childSock))
	}

	parentOK, err := h.Run(t, repoPath, "workspace", "exec", parentID, "--", "sh", "-c",
		"test -S /tmp/ssh-agent.sock && echo OK; sleep 0.05")
	if err != nil {
		t.Fatalf("parent socket check: %v\noutput: %s", err, parentOK)
	}
	if !strings.Contains(string(parentOK), "OK") {
		t.Errorf("parent: /tmp/ssh-agent.sock not a socket, got %q", string(parentOK))
	}

	childOK, err := h.Run(t, repoPath, "workspace", "exec", childID, "--", "sh", "-c",
		"test -S /tmp/ssh-agent.sock && echo OK; sleep 0.05")
	if err != nil {
		t.Fatalf("child socket check: %v\noutput: %s", err, childOK)
	}
	if !strings.Contains(string(childOK), "OK") {
		t.Errorf("child: /tmp/ssh-agent.sock not a socket, got %q", string(childOK))
	}
}
