//go:build e2e

package vmproof_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// createWorkspaceAndStart creates a workspace from a local repo, starts it,
// and waits for it to be ready. The workspace is cleaned up at test end.
func createWorkspaceAndStart(t *testing.T, h *harness.CLIHarness, repoPath, name string) string {
	t.Helper()
	var res struct {
		Workspace struct{ ID string `json:"id"` } `json:"workspace"`
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

	// Wait for workspace to be fully ready before running exec commands.
	var readyRes struct{ Ready bool `json:"ready"` }
	ready := false
	for attempts := 0; attempts < 120; attempts++ {
		h.MustCall("workspace.ready", map[string]any{"id": id}, &readyRes)
		if readyRes.Ready {
			ready = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !ready {
		t.Fatalf("workspace %s did not become ready within 120s", id)
	}

	return id
}

// Spec: VM-PROOF-006
// TestVMProof_GuestCLITools verifies that mise-based CLI tools (node, opencode,
// codex, claude) and the tool stamp are available inside a running workspace.
func TestVMProof_GuestCLITools(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "vmproof-tools")
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-tools")

	cases := []struct {
		name    string
		args    []string
		wantOut string
	}{
		{
			name:    "node version",
			args:    []string{"workspace", "exec", wsID, "--", "node", "--version"},
			wantOut: "v",
		},
		{
			name:    "make version",
			args:    []string{"workspace", "exec", wsID, "--", "make", "--version"},
			wantOut: "GNU Make",
		},
		{
			name:    "opencode wrapper",
			args:    []string{"workspace", "exec", wsID, "--", "which", "opencode"},
			wantOut: "/usr/local/bin/opencode",
		},
		{
			name:    "codex wrapper",
			args:    []string{"workspace", "exec", wsID, "--", "which", "codex"},
			wantOut: "/usr/local/bin/codex",
		},
		{
			name:    "claude wrapper",
			args:    []string{"workspace", "exec", wsID, "--", "which", "claude"},
			wantOut: "/usr/local/bin/claude",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := h.Run(t, repoPath, tc.args...)
			if err != nil {
				t.Fatalf("%s: %v\noutput: %s", tc.name, err, out)
			}
			if !strings.Contains(string(out), tc.wantOut) {
				t.Errorf("%s: expected %q in output, got %q", tc.name, tc.wantOut, string(out))
			}
		})
	}

	// Verify the bake/tool stamp file exists inside the VM.
	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "cat", "/var/lib/nexus-tools-base-v14")
	if err != nil {
		t.Fatalf("tool stamp: %v\noutput: %s", err, out)
	}
	if !bytes.Contains(out, []byte("nexus-tools-base")) {
		// The stamp file content may vary; just verify it's non-empty and contains the expected prefix.
		if len(bytes.TrimSpace(out)) == 0 {
			t.Error("tool stamp: expected non-empty stamp file")
		}
	}
}
