//go:build e2e

package workspace_test

import (
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: VM-005, VM-PROOF-002, INV-007, INV-008, INV-009, INV-011, WS-040, WS-041, WS-043, WS-045, WS-048, WS-049, WS-050, WS-051, WS-052, WS-053, WS-054, WS-055, WS-056
func TestWorkspaceLifecycle(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	clientRepo := harness.MakeLocalGitRepo(t, "lifecycle")
	cfg := harness.MirrorProfileConfigHome(t)
	_, remoteRepo := harness.MirrorGitCheckoutToDaemon(t, h, cfg, clientRepo, "proj-lifecycle")

	// 1. Create
	var createRes struct {
		Workspace struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          remoteRepo,
			"ref":           "main",
			"workspaceName": "lifecycle-test",
		},
	}, &createRes)
	id := createRes.Workspace.ID
	if id == "" {
		t.Fatal("create: got empty workspace id")
	}

	// 2. Verify it appears in list
	var listRes struct {
		Workspaces []struct {
			ID string `json:"id"`
		} `json:"workspaces"`
	}
	h.MustCall("workspace.list", nil, &listRes)
	found := false
	for _, ws := range listRes.Workspaces {
		if ws.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("list: workspace %s not found after create", id)
	}

	// 3. Start
	var startRes struct {
		Workspace struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.start", map[string]any{"id": id}, &startRes)
	if startRes.Workspace.ID == "" {
		t.Fatal("start: got empty workspace id in response")
	}
	if startRes.Workspace.ID != id {
		t.Fatalf("start: response workspace id %q does not match created id %q", startRes.Workspace.ID, id)
	}
	if startRes.Workspace.State == "" {
		t.Fatal("start: got empty workspace state in response")
	}

	// 4. Verify state is running
	var getRes struct {
		Workspace struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.info", map[string]any{"id": id}, &getRes)
	if getRes.Workspace.State != "running" {
		t.Fatalf("after start: expected state running, got %q", getRes.Workspace.State)
	}

	// 5. Stop
	var stopRes struct {
		Stopped bool `json:"stopped"`
	}
	h.MustCall("workspace.stop", map[string]any{"id": id}, &stopRes)
	if !stopRes.Stopped {
		t.Fatal("stop: expected stopped=true")
	}

	// 6. Verify state is stopped
	h.MustCall("workspace.info", map[string]any{"id": id}, &getRes)
	if getRes.Workspace.State != "stopped" {
		t.Fatalf("after stop: expected state stopped, got %q", getRes.Workspace.State)
	}

	// 7. Remove
	var removeRes struct {
		Removed bool `json:"removed"`
	}
	h.MustCall("workspace.remove", map[string]any{"id": id}, &removeRes)
	if !removeRes.Removed {
		t.Fatal("remove: expected removed=true")
	}

	// 8. Verify it no longer appears in list
	h.MustCall("workspace.list", nil, &listRes)
	for _, ws := range listRes.Workspaces {
		if ws.ID == id {
			t.Fatalf("list: workspace %s still present after remove", id)
		}
	}
}
