//go:build e2e

package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

func TestCLI_ProjectCreateListGetRemove(t *testing.T) {
	t.Parallel()
	h := harness.NewCLIHarness(t)
	root := t.TempDir()
	base := filepath.Base(root)
	if len(base) > 8 {
		base = base[len(base)-8:]
	}
	name := "test-proj-" + base

	out, err := h.Run(t, root, "project", "create", "--name", name, "--repo", root)
	if err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "created project") {
		t.Fatalf("project create: unexpected output: %s", out)
	}

	out, err = h.Run(t, root, "project", "list")
	if err != nil {
		t.Fatalf("project list: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), name) {
		t.Fatalf("project list: expected project name in output: %s", out)
	}

	out, err = h.Run(t, root, "project", "get", name)
	if err != nil {
		t.Fatalf("project get: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "name:") || !strings.Contains(s, name) {
		t.Fatalf("project get: unexpected output: %s", out)
	}

	out, err = h.Run(t, root, "project", "remove", name, "--force")
	if err != nil {
		t.Fatalf("project remove: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "removed project") {
		t.Fatalf("project remove: unexpected output: %s", out)
	}
}

// Spec: VM-001, VM-002, VM-004, VM-005, VM-PROOF-001, VM-PROOF-005
func TestCLI_WorkspaceShellAndExec(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	clientRepo := harness.MakeLocalGitRepo(t, "shell")
	_, daemonRepo := h.MirrorGitToDaemon(t, clientRepo, "proj-cli-shell")

	var createRes struct {
		Workspace struct {
			ID            string `json:"id"`
			WorkspaceName string `json:"workspaceName"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          daemonRepo,
			"ref":           "main",
			"workspaceName": "cli-shell",
		},
	}, &createRes)
	wsID := createRes.Workspace.ID
	if wsID == "" {
		t.Fatal("workspace.create: empty id")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": wsID}, nil)
	})

	out, err := h.Run(t, clientRepo, "workspace", "list")
	if err != nil {
		t.Fatalf("workspace list: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), wsID) {
		t.Fatalf("workspace list: expected id %q in output: %s", wsID, out)
	}

	out, err = h.Run(t, clientRepo, "workspace", "info", "cli-shell")
	if err != nil {
		t.Fatalf("workspace info: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), wsID) {
		t.Fatalf("workspace info: expected id in output: %s", out)
	}

	out, err = h.Run(t, clientRepo, "workspace", "start", wsID)
	if err != nil {
		t.Fatalf("workspace start: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "started workspace") {
		t.Fatalf("workspace start: unexpected output: %s", out)
	}

	shellOut, err := h.RunWithStdin(t, clientRepo, "pwd\nexit\n", "workspace", "shell", "cli-shell", "--workdir", "/workspace")
	if err != nil {
		t.Fatalf("workspace shell: %v\n%s", err, shellOut)
	}

	out, err = h.Run(t, clientRepo, "workspace", "exec", "cli-shell", "--", "sh", "-c", "echo cli-exec-ok")
	if err != nil {
		t.Fatalf("workspace exec: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("cli-exec-ok")) {
		t.Fatalf("workspace exec: want cli-exec-ok in output: %s", out)
	}

	out, err = h.Run(t, clientRepo, "workspace", "stop", wsID)
	if err != nil {
		t.Fatalf("workspace stop: %v\n%s", err, out)
	}
}
