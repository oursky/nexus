//go:build e2e

package pty_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// createWorkspaceLocalRepo creates a workspace with a local repo path (no Mutagen needed).
func createWorkspaceLocalRepo(t *testing.T, h *harness.CLIHarness, repoPath, name string) string {
	t.Helper()
	var res struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          repoPath,
			"ref":           "main",
			"workspaceName": name,
		},
	}, &res)
	id := res.Workspace.ID
	if id == "" {
		t.Fatalf("workspace.create %s: empty id", name)
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": id}, nil)
	})
	h.MustCall("workspace.start", map[string]any{"id": id}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, id)
	return id
}

// Spec: VM-002, VM-003, VM-PROOF-005, PTY-001, PTY-002, PTY-005, PTY-006, PTY-007, PTY-009
// TestPTY_ExecPWD verifies that workspace exec runs in the workspace's repo directory.
func TestPTY_ExecPWD(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "exec-pwd")
	wsID := createWorkspaceLocalRepo(t, h, repoPath, "exec-pwd")

	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "pwd")
	if err != nil {
		t.Fatalf("workspace exec pwd: %v\noutput: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	// VM backend mounts the repo at /workspace; process backend uses the host
	// repo directory name.
	wantSuffix := filepath.Base(repoPath)
	if harness.IsVMBackend() {
		if got != "/workspace" {
			t.Errorf("pwd: expected /workspace on VM backend, got %q", got)
		}
	} else if !strings.Contains(got, wantSuffix) {
		t.Errorf("pwd: expected output to contain %q, got %q", wantSuffix, got)
	}
}

// Spec: VM-001, VM-PROOF-001, PTY-001, PTY-002, PTY-005, PTY-006, PTY-007, PTY-009
// TestPTY_ExecEcho verifies stdout is streamed back from workspace exec.
func TestPTY_ExecEcho(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "exec-echo")
	wsID := createWorkspaceLocalRepo(t, h, repoPath, "exec-echo")

	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "echo", "NEXUS_EXEC_OK")
	if err != nil {
		t.Fatalf("workspace exec echo: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "NEXUS_EXEC_OK") {
		t.Errorf("echo: expected NEXUS_EXEC_OK in output, got %q", string(out))
	}
}

// TestPTY_ExecGitBranch verifies git branch reports correctly in the workspace.
func TestPTY_ExecGitBranch(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "exec-git-branch")
	wsID := createWorkspaceLocalRepo(t, h, repoPath, "exec-git-branch")

	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("workspace exec git branch: %v\noutput: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "main" {
		t.Errorf("git branch: expected main, got %q", got)
	}
}

// TestPTY_ExecWriteAndReadFile verifies file writes are visible from workspace exec.
// Skipped on VM backend because guest writes do not sync back to the host filesystem.
func TestPTY_ExecWriteAndReadFile(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	if harness.IsVMBackend() {
		t.Skip("VM backend: guest writes do not sync to host filesystem")
	}
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "exec-file")
	wsID := createWorkspaceLocalRepo(t, h, repoPath, "exec-file")

	marker := "nexus_write_test_content_12345"
	tmpFile := filepath.Join(repoPath, "e2e_write_test.txt")

	// Write via exec. Use a relative path — the sandbox backend runs in the
	// workspace repo directory, which maps to repoPath on the host.
	_, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c", "echo "+marker+" > ./e2e_write_test.txt")
	if err != nil {
		t.Fatalf("workspace exec write: %v", err)
	}

	// Read the file from host to verify it was written.
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !strings.Contains(string(content), marker) {
		t.Errorf("file content: expected %q, got %q", marker, string(content))
	}
}

// TestPTY_ExecExitCode verifies non-zero exit codes propagate correctly.
func TestPTY_ExecExitCode(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "exec-exit")
	wsID := createWorkspaceLocalRepo(t, h, repoPath, "exec-exit")

	// sh -c 'exit 42' should cause the CLI to exit with 42 which means Run() returns an error.
	_, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c", "exit 42")
	if err == nil {
		t.Error("exec exit 42: expected non-nil error, got nil")
	}
}

// Spec: VM-001, VM-PROOF-001, PTY-001, PTY-002, PTY-005, PTY-006, PTY-007, PTY-009
// TestPTY_ShellNonInteractiveScript verifies workspace shell with stdin pipe runs a script.
func TestPTY_ShellNonInteractiveScript(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "shell-script")
	wsID := createWorkspaceLocalRepo(t, h, repoPath, "shell-script")

	out, err := h.RunWithStdin(t, repoPath, "echo SHELL_SCRIPT_OK\n", "workspace", "shell", wsID)
	if err != nil {
		t.Fatalf("workspace shell script: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "SHELL_SCRIPT_OK") {
		t.Errorf("shell script: expected SHELL_SCRIPT_OK in output, got %q", string(out))
	}
}

// Spec: PTY-017, PTY-018
// TestPTY_ListSession verifies pty.list shows a created session.
func TestPTY_ListSession(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "pty-list")
	var wsRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo": repoPath, "ref": "main", "workspaceName": "pty-list-test",
		},
	}, &wsRes)
	wsID := wsRes.Workspace.ID
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": wsID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": wsID}, nil)
	harness.WaitForWorkspaceReady(t, h, wsID)

	var sessRes struct {
		ID string `json:"id"`
	}
	h.MustCall("pty.create", map[string]any{
		"workspaceId": wsID,
		"name":        "list-test-session",
		"cols":        80,
		"rows":        24,
	}, &sessRes)
	sessionID := sessRes.ID
	if sessionID == "" {
		t.Fatal("pty.create: empty session id")
	}
	t.Cleanup(func() {
		_ = h.Call("pty.close", map[string]any{"sessionId": sessionID}, nil)
	})

	var listRes struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	h.MustCall("pty.list", map[string]any{"workspaceId": wsID}, &listRes)
	found := false
	for _, s := range listRes.Sessions {
		if s.ID == sessionID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pty.list: session %s not found after create", sessionID)
	}
}
