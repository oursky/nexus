//go:build e2e

package vmproof_test

import (
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: VM-024 (spotlight restart robustness)
// TestVMProof_Spotlight_AcrossRestart verifies that after a workspace is stopped
// and restarted, the network stack comes back cleanly and a port previously
// served inside the VM is reachable again after the server is restarted.
func TestVMProof_Spotlight_AcrossRestart(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)

	repoPath := harness.MakeGitRepoWithContent(t, "vmproof-spotlight-restart", map[string]string{
		"README.md": "spotlight restart test\n",
	})

	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-spotlight-restart")

	// Write a test file and start an HTTP server in the workspace.
	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"echo 'spotlight-test' > /tmp/test.txt && nohup python3 -m http.server 18888 --directory /tmp >/tmp/httpd.log 2>&1 & sleep 1")
	if err != nil {
		t.Fatalf("start HTTP server (first boot): %v\n%s", err, out)
	}

	// Verify the HTTP server is reachable inside the VM before restart.
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"curl -sf http://localhost:18888/test.txt; sleep 0.05")
	if err != nil {
		t.Fatalf("curl before restart: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "spotlight-test") {
		t.Fatalf("before restart: expected 'spotlight-test' in curl output, got %q", string(out))
	}

	// Stop the workspace.
	h.MustCall("workspace.stop", map[string]any{"id": wsID}, nil)

	// Restart the workspace and wait for ready.
	h.MustCall("workspace.start", map[string]any{"id": wsID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, wsID)

	// The HTTP server does not survive restart — start it again.
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"echo 'spotlight-test' > /tmp/test.txt && nohup python3 -m http.server 18888 --directory /tmp >/tmp/httpd.log 2>&1 & sleep 1")
	if err != nil {
		t.Fatalf("start HTTP server (after restart): %v\n%s", err, out)
	}

	// Verify the HTTP server is reachable again — networking stack recovered cleanly.
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"curl -sf http://localhost:18888/test.txt; sleep 0.05")
	if err != nil {
		t.Fatalf("curl after restart: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "spotlight-test") {
		t.Fatalf("after restart: expected 'spotlight-test' in curl output, got %q", string(out))
	}
}

// Spec: VM-024b (spotlight fork independence)
// TestVMProof_Spotlight_AcrossFork verifies that a forked workspace has an
// independent network stack: an HTTP server started in the fork on the same
// port serves fork-specific content and does not share state with the parent.
func TestVMProof_Spotlight_AcrossFork(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)

	repoPath := harness.MakeGitRepoWithContent(t, "vmproof-spotlight-fork", map[string]string{
		"README.md": "spotlight fork test\n",
	})

	// Create and start parent workspace.
	var parentRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "spotlight-fork-parent"},
	}, &parentRes)
	parentID := parentRes.Workspace.ID
	if parentID == "" {
		t.Fatal("create parent: empty id")
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": parentID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": parentID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, parentID)

	// Start HTTP server in parent with parent-specific content.
	out, err := h.Run(t, repoPath, "workspace", "exec", parentID, "--", "sh", "-c",
		"echo 'parent-content' > /tmp/probe.txt && nohup python3 -m http.server 18888 --directory /tmp >/tmp/httpd.log 2>&1 & sleep 1")
	if err != nil {
		t.Fatalf("parent: start HTTP server: %v\n%s", err, out)
	}

	// Verify parent HTTP server is reachable.
	out, err = h.Run(t, repoPath, "workspace", "exec", parentID, "--", "sh", "-c",
		"curl -sf http://localhost:18888/probe.txt; sleep 0.05")
	if err != nil {
		t.Fatalf("parent: curl before fork: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "parent-content") {
		t.Fatalf("parent: expected 'parent-content', got %q", string(out))
	}

	// Fork the parent.
	var forkRes struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id": parentID, "childWorkspaceName": "spotlight-fork-child", "childRef": "main",
	}, &forkRes)
	if !forkRes.Forked {
		t.Fatal("fork: expected forked=true")
	}
	childID := forkRes.Workspace.ID
	if childID == "" {
		t.Fatal("fork: empty child id")
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": childID}, nil) })

	// Start the forked workspace.
	h.MustCall("workspace.start", map[string]any{"id": childID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, childID)

	// Re-establish parent's HTTP server after fork: CheckpointFork stops then
	// restarts the parent VM to ensure filesystem consistency, which kills all
	// in-guest processes and clears /tmp (tmpfs). The parent restarts
	// asynchronously, so we must wait for it to be ready before exec-ing.
	harness.WaitForWorkspaceReady(t, h.Harness, parentID)
	out, err = h.Run(t, repoPath, "workspace", "exec", parentID, "--", "sh", "-c",
		"echo 'parent-content' > /tmp/probe.txt && nohup python3 -m http.server 18888 --directory /tmp >/tmp/httpd.log 2>&1 & sleep 1")
	if err != nil {
		t.Fatalf("parent: restart HTTP server after fork: %v\n%s", err, out)
	}

	// Start HTTP server in fork with fork-specific content on the same port.
	out, err = h.Run(t, repoPath, "workspace", "exec", childID, "--", "sh", "-c",
		"echo 'fork-content' > /tmp/probe.txt && nohup python3 -m http.server 18888 --directory /tmp >/tmp/httpd.log 2>&1 & sleep 1")
	if err != nil {
		t.Fatalf("fork: start HTTP server: %v\n%s", err, out)
	}

	// Verify fork's HTTP server returns fork-specific content.
	out, err = h.Run(t, repoPath, "workspace", "exec", childID, "--", "sh", "-c",
		"curl -sf http://localhost:18888/probe.txt; sleep 0.05")
	if err != nil {
		t.Fatalf("fork: curl: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "fork-content") {
		t.Fatalf("fork: expected 'fork-content', got %q", string(out))
	}

	// Verify parent's HTTP server still returns parent-specific content (independent networks).
	out, err = h.Run(t, repoPath, "workspace", "exec", parentID, "--", "sh", "-c",
		"curl -sf http://localhost:18888/probe.txt; sleep 0.05")
	if err != nil {
		t.Fatalf("parent: curl after fork: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "parent-content") {
		t.Fatalf("parent after fork: expected 'parent-content', got %q", string(out))
	}
}
