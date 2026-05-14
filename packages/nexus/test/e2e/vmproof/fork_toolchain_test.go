//go:build e2e

package vmproof_test

import (
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: VM-018 (fork toolchain), VM-019 (fork docker)
// TestVMProof_ForkToolchain verifies that docker, node, make, git, and bridge
// networking all work inside a fork (hybrid-overlay) workspace.
func TestVMProof_ForkToolchain(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)

	repoPath := harness.MakeGitRepoWithContent(t, "vmproof-fork-toolchain", map[string]string{
		"README.md": "fork toolchain test\n",
	})

	// Create and start the parent workspace.
	var parentRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          repoPath,
			"ref":           "main",
			"workspaceName": "fork-toolchain-parent",
		},
	}, &parentRes)
	parentID := parentRes.Workspace.ID
	if parentID == "" {
		t.Fatal("create parent: empty id")
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": parentID}, nil) })

	h.MustCall("workspace.start", map[string]any{"id": parentID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, parentID)

	waitGuestDockerReachable(t, h, repoPath, parentID)

	// Fork the parent workspace.
	var forkRes struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id":                 parentID,
		"childWorkspaceName": "fork-toolchain-child",
		"childRef":           "main",
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

	waitGuestDockerReachable(t, h, repoPath, childID)

	cases := []struct {
		name    string
		cmd     string
		wantOut string
	}{
		{
			name:    "docker ps header",
			cmd:     "docker ps; sleep 0.05",
			wantOut: "CONTAINER ID",
		},
		{
			name:    "node version",
			cmd:     "node --version; sleep 0.05",
			wantOut: "v20.",
		},
		{
			name:    "make version",
			cmd:     "make --version | head -1; sleep 0.05",
			wantOut: "GNU Make",
		},
		{
			name:    "git version",
			cmd:     "git --version; sleep 0.05",
			wantOut: "git version",
		},
		{
			name:    "bridge networking",
			cmd:     "ip link add br-fork-test type bridge && echo OK && ip link del br-fork-test; sleep 0.05",
			wantOut: "OK",
		},
		{
			name:    "workspace mount",
			cmd:     "ls /workspace; sleep 0.05",
			wantOut: "README.md",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := h.Run(t, repoPath, "workspace", "exec", childID, "--", "sh", "-c", tc.cmd)
			if err != nil {
				t.Fatalf("%s: %v\noutput: %s", tc.name, err, out)
			}
			if !strings.Contains(string(out), tc.wantOut) {
				t.Errorf("%s: expected %q in output, got %q", tc.name, tc.wantOut, string(out))
			}
		})
	}
}
