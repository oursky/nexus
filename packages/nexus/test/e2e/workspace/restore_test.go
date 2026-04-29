//go:build e2e

package workspace_test

import (
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: WS-063, WS-064, WS-065, WS-066
// TestWorkspaceRestore verifies that a stopped workspace can be restored via
// workspace.restore and ends up in the restored state.
//
// Note: workspace.restore resumes from a snapshot taken during workspace.stop
// (the daemon persists VM state on stop). There is no separate snapshot RPC.
func TestWorkspaceRestore(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)

	clientRepo := harness.MakeLocalGitRepo(t, "restore")
	cfg := harness.MirrorProfileConfigHome(t)
	_, remoteRepo := harness.MirrorGitCheckoutToDaemon(t, h, cfg, clientRepo, "proj-restore")

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
			"workspaceName": "restore-test",
		},
	}, &createRes)
	id := createRes.Workspace.ID
	if id == "" {
		t.Fatal("create: got empty workspace id")
	}

	// 2. Start
	var startRes struct {
		Workspace struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.start", map[string]any{"id": id}, &startRes)

	// 3. Wait for ready, then verify running
	harness.WaitForWorkspaceReady(t, h, id)
	var infoRes struct {
		Workspace struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.info", map[string]any{"id": id}, &infoRes)
	if infoRes.Workspace.State != "running" {
		t.Fatalf("after start: expected state running, got %q", infoRes.Workspace.State)
	}

	// 4. Stop (persists snapshot state)
	var stopRes struct {
		Stopped bool `json:"stopped"`
	}
	h.MustCall("workspace.stop", map[string]any{"id": id}, &stopRes)
	if !stopRes.Stopped {
		t.Fatal("stop: expected stopped=true")
	}

	// 5. Restore from snapshot
	var restoreRes struct {
		Restored  bool `json:"restored"`
		Workspace struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.restore", map[string]any{"id": id}, &restoreRes)
	if !restoreRes.Restored {
		t.Fatal("restore: expected restored=true")
	}

	// 6. Verify state after restore — daemon sets state to "restored"
	h.MustCall("workspace.info", map[string]any{"id": id}, &infoRes)
	if infoRes.Workspace.State != "restored" {
		t.Fatalf("after restore: expected state restored, got %q", infoRes.Workspace.State)
	}

	// 7. Cleanup
	// Stop after restore may fail on VM backend if the workspace was not
	// actually re-spawned; tolerate the error and proceed with remove.
	_ = h.Call("workspace.stop", map[string]any{"id": id}, &stopRes)
	var removeRes struct {
		Removed bool `json:"removed"`
	}
	h.MustCall("workspace.remove", map[string]any{"id": id}, &removeRes)
	if !removeRes.Removed {
		t.Fatal("remove: expected removed=true")
	}
}
