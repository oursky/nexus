//go:build e2e

package workspace_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/inizio/nexus/packages/nexus/test/e2e/harness"
)

// createWorkspaceForSM creates a workspace for state machine tests using a local repo.
func createWorkspaceForSM(t *testing.T, h *harness.Harness, name string) string {
	t.Helper()
	repoPath := harness.MakeLocalGitRepo(t, "sm-"+name)
	var res struct {
		Workspace struct{ ID string `json:"id"` } `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": name},
	}, &res)
	id := res.Workspace.ID
	if id == "" {
		t.Fatalf("createWorkspaceForSM %s: empty id", name)
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": id}, nil) })
	return id
}

// rpcErrorCode extracts the semantic HTTP-style error code from an RPC error.
// The transport layer wraps all handler errors in JSON-RPC -32603 with the
// original message embedded, so we check the full error string for 404/400/500.
func rpcErrorCode(err error) int {
	if err == nil {
		return 0
	}
	s := err.Error()
	lower := strings.ToLower(s)
	// 404 / not found.
	if strings.Contains(s, "404") || strings.Contains(lower, "not found") {
		return 404
	}
	// 400 / invalid params.
	if strings.Contains(s, "400") || strings.Contains(lower, "invalid params") || strings.Contains(lower, "invalid_params") {
		return 400
	}
	// 500 / internal.
	if strings.Contains(s, "500") || strings.Contains(lower, "internal error") {
		return 500
	}
	// Return the raw JSON-RPC code as fallback.
	var code int
	if _, scanErr := fmt.Sscanf(s, "rpc error %d:", &code); scanErr == nil {
		return code
	}
	return 0
}

// TestLifecycle_StartAndStop verifies a workspace can be started and stopped.
func TestLifecycle_StartAndStop(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	id := createWorkspaceForSM(t, h, "sm-start-stop")

	h.MustCall("workspace.start", map[string]any{"id": id}, nil)

	var infoRes struct {
		Workspace struct{ State string `json:"state"` } `json:"workspace"`
	}
	h.MustCall("workspace.info", map[string]any{"id": id}, &infoRes)
	if infoRes.Workspace.State != "running" {
		t.Errorf("after start: state=%q, want running", infoRes.Workspace.State)
	}

	var stopRes struct{ Stopped bool `json:"stopped"` }
	h.MustCall("workspace.stop", map[string]any{"id": id}, &stopRes)
	if !stopRes.Stopped {
		t.Error("stop: expected stopped=true")
	}

	h.MustCall("workspace.info", map[string]any{"id": id}, &infoRes)
	if infoRes.Workspace.State != "stopped" {
		t.Errorf("after stop: state=%q, want stopped", infoRes.Workspace.State)
	}
}

// TestLifecycle_ReadyState verifies workspace.ready follows start/stop transitions.
func TestLifecycle_ReadyState(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	id := createWorkspaceForSM(t, h, "sm-ready")

	var readyRes struct{ Ready bool `json:"ready"` }
	h.MustCall("workspace.ready", map[string]any{"id": id}, &readyRes)
	if readyRes.Ready {
		t.Error("ready before start: expected false, got true")
	}

	h.MustCall("workspace.start", map[string]any{"id": id}, nil)

	h.MustCall("workspace.ready", map[string]any{"id": id}, &readyRes)
	if !readyRes.Ready {
		t.Error("ready after start: expected true, got false")
	}
}

// TestLifecycle_RestoreFromStopped verifies a stopped workspace can be restored.
func TestLifecycle_RestoreFromStopped(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	id := createWorkspaceForSM(t, h, "sm-restore")

	h.MustCall("workspace.start", map[string]any{"id": id}, nil)
	h.MustCall("workspace.stop", map[string]any{"id": id}, nil)

	var restoreRes struct {
		Restored  bool `json:"restored"`
		Workspace struct{ State string `json:"state"` } `json:"workspace"`
	}
	h.MustCall("workspace.restore", map[string]any{"id": id}, &restoreRes)
	if !restoreRes.Restored {
		t.Error("restore: expected restored=true")
	}

	var infoRes struct {
		Workspace struct{ State string `json:"state"` } `json:"workspace"`
	}
	h.MustCall("workspace.info", map[string]any{"id": id}, &infoRes)
	if infoRes.Workspace.State != "restored" {
		t.Errorf("after restore: state=%q, want restored", infoRes.Workspace.State)
	}
}

// TestLifecycle_RemoveNotInList verifies a removed workspace is absent from workspace.list.
func TestLifecycle_RemoveNotInList(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "sm-remove")
	var res struct {
		Workspace struct{ ID string `json:"id"` } `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "sm-remove-test"},
	}, &res)
	id := res.Workspace.ID

	var removeRes struct{ Removed bool `json:"removed"` }
	h.MustCall("workspace.remove", map[string]any{"id": id}, &removeRes)
	if !removeRes.Removed {
		t.Error("remove: expected removed=true")
	}

	var listRes struct {
		Workspaces []struct{ ID string `json:"id"` } `json:"workspaces"`
	}
	h.MustCall("workspace.list", nil, &listRes)
	for _, ws := range listRes.Workspaces {
		if ws.ID == id {
			t.Errorf("list: removed workspace %s still present", id)
		}
	}
}

// TestLifecycle_NotFound verifies workspace.info for an unknown id returns a 404 error.
func TestLifecycle_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	err := h.Call("workspace.info", map[string]any{"id": "ws-nonexistent-999"}, nil)
	if err == nil {
		t.Fatal("workspace.info with unknown id: expected error, got nil")
	}
	code := rpcErrorCode(err)
	if code != 404 {
		t.Errorf("workspace.info unknown: expected 404 error, got code=%d err=%v", code, err)
	}
}

// TestLifecycle_StartNotFound verifies workspace.start for an unknown id returns a 404 error.
func TestLifecycle_StartNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	err := h.Call("workspace.start", map[string]any{"id": "ws-nonexistent-999"}, nil)
	if err == nil {
		t.Fatal("workspace.start with unknown id: expected error, got nil")
	}
	code := rpcErrorCode(err)
	if code != 404 {
		t.Errorf("workspace.start unknown: expected 404 error, got code=%d err=%v", code, err)
	}
}

// TestLifecycle_ForkRequiresChildRef verifies workspace.fork without childRef is rejected.
func TestLifecycle_ForkRequiresChildRef(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	id := createWorkspaceForSM(t, h, "sm-fork-noref")

	err := h.Call("workspace.fork", map[string]any{"id": id, "childRef": ""}, nil)
	if err == nil {
		t.Fatal("workspace.fork with empty childRef: expected error, got nil")
	}
}
