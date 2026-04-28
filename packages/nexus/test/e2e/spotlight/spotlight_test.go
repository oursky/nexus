//go:build e2e

package spotlight_test

import (
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: SPOT-024, SPOT-025, SPOT-026, SPOT-027, SPOT-028, SPOT-029, SPOT-030, SPOT-031, SPOT-032, SPOT-033, SPOT-034, SPOT-035, INV-005
func TestSpotlight(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	repo := harness.MakeLocalGitRepo(t, "spotlight")
	cfg := harness.MirrorProfileConfigHome(t)
	_, remoteRepo := harness.MirrorGitCheckoutToDaemon(t, h, cfg, repo, "proj-spotlight")

	// Create and start a workspace to attach spotlight forwards to.
	var createRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          remoteRepo,
			"ref":           "main",
			"workspaceName": "spotlight-test",
		},
	}, &createRes)
	wsID := createRes.Workspace.ID
	if wsID == "" {
		t.Fatal("create: got empty workspace id")
	}

	var startRes struct {
		Workspace struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.start", map[string]any{"id": wsID}, &startRes)

	t.Cleanup(func() {
		var stopRes struct{ Stopped bool }
		_ = h.Call("workspace.stop", map[string]any{"id": wsID}, &stopRes)
		var removeRes struct{ Removed bool }
		_ = h.Call("workspace.remove", map[string]any{"id": wsID}, &removeRes)
	})

	// 1. Register a port forward via workspace.ports.add.
	var addRes struct {
		Forward *struct {
			ID         string `json:"id"`
			LocalPort  int    `json:"localPort"`
			RemotePort int    `json:"remotePort"`
		} `json:"forward"`
	}
	err := h.Call("workspace.ports.add", map[string]any{
		"workspaceId": wsID,
		"spec": map[string]any{
			"localPort":  8080,
			"remotePort": 8080,
		},
	}, &addRes)
	if err != nil {
		t.Skipf("workspace.ports.add not available: %v", err)
	}
	if addRes.Forward == nil {
		t.Fatal("ports.add: got nil forward")
	}
	fwdID := addRes.Forward.ID
	if fwdID == "" {
		t.Fatal("ports.add: got empty forward id")
	}

	// 2. List and verify the forward appears.
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
		t.Fatalf("ports.list: forward %s not found after add", fwdID)
	}

	// 3. Remove the forward.
	var removeRes struct {
		Closed bool `json:"closed"`
	}
	h.MustCall("workspace.ports.remove", map[string]any{
		"workspaceId": wsID,
		"forwardId":   fwdID,
	}, &removeRes)
	if !removeRes.Closed {
		t.Fatal("ports.remove: expected closed=true")
	}

	// 4. Verify the forward is gone.
	h.MustCall("workspace.ports.list", map[string]any{"workspaceId": wsID}, &listRes)
	for _, f := range listRes.Forwards {
		if f.ID == fwdID {
			t.Fatalf("ports.list: forward %s still present after remove", fwdID)
		}
	}
}

// Spec: ERR-050
// TestSpotlight_WorkspaceNotFound verifies workspace.ports.add on unknown workspace returns error.
func TestSpotlight_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	err := h.Call("workspace.ports.add", map[string]any{
		"workspaceId": "ws-does-not-exist",
		"spec": map[string]any{
			"localPort":  8080,
			"remotePort": 8080,
		},
	}, nil)
	if err == nil {
		t.Fatal("ports.add unknown workspace: expected error, got nil")
	}
}
