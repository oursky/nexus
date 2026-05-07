//go:build e2e

package workspace_test

import (
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: WS-059, ERR-011
// TestErrors_WorkspaceNotFound verifies workspace operations on unknown IDs return 404.
func TestErrors_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	const unknownID = "ws-does-not-exist-00000"

	cases := []struct {
		method string
		params map[string]any
	}{
		{"workspace.info", map[string]any{"id": unknownID}},
		{"workspace.start", map[string]any{"id": unknownID}},
		{"workspace.stop", map[string]any{"id": unknownID}},
		{"workspace.restore", map[string]any{"id": unknownID}},
		{"workspace.remove", map[string]any{"id": unknownID}},
		{"workspace.fork", map[string]any{"id": unknownID, "childRef": "branch"}},
		{"workspace.ready", map[string]any{"id": unknownID}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.method, func(t *testing.T) {
			err := h.Call(tc.method, tc.params, nil)
			if err == nil {
				t.Fatalf("%s with unknown id: expected error, got nil", tc.method)
			}
			code := rpcErrorCode(err)
			if code != 404 {
				t.Errorf("%s unknown id: expected 404, got code=%d err=%v", tc.method, code, err)
			}
		})
	}
}

// Spec: WS-044, ERR-013
// TestErrors_CreateMissingRequiredFields verifies workspace.create rejects missing fields.
func TestErrors_CreateMissingRequiredFields(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)

	// Missing repo.
	err := h.Call("workspace.create", map[string]any{
		"spec": map[string]any{"workspaceName": "test", "ref": "main"},
	}, nil)
	if err == nil {
		t.Error("create without repo: expected error, got nil")
	}

	// Missing workspaceName.
	err = h.Call("workspace.create", map[string]any{
		"spec": map[string]any{"repo": "/tmp/someRepo", "ref": "main"},
	}, nil)
	if err == nil {
		t.Error("create without workspaceName: expected error, got nil")
	}
}

// Spec: ERR-004
// TestErrors_MissingIDParam verifies methods that require id reject empty/missing id.
func TestErrors_MissingIDParam(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)

	cases := []struct {
		method string
	}{
		{"workspace.info"},
		{"workspace.start"},
		{"workspace.stop"},
		{"workspace.remove"},
		{"workspace.ready"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.method, func(t *testing.T) {
			err := h.Call(tc.method, map[string]any{"id": ""}, nil)
			if err == nil {
				t.Fatalf("%s with empty id: expected error, got nil", tc.method)
			}
		})
	}
}

// Spec: ERR-004
// TestErrors_SpotlightStopMissingWorkspaceID verifies spotlight.stop with no workspaceId returns 400.
func TestErrors_SpotlightStopMissingWorkspaceID(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	err := h.Call("spotlight.stop", map[string]any{}, nil)
	if err == nil {
		t.Fatal("spotlight.stop with empty workspaceId: expected error, got nil")
	}
}

// Spec: INV-016
// TestErrors_SpotlightStopUnknownWorkspace verifies spotlight.stop on a workspace with no
// active forwards succeeds (idempotent — nothing to stop is not an error).
func TestErrors_SpotlightStopUnknownWorkspace(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	err := h.Call("spotlight.stop", map[string]any{"workspaceId": "ws-does-not-exist"}, nil)
	if err != nil {
		t.Fatalf("spotlight.stop on workspace with no forwards: expected nil, got %v", err)
	}
}

// Spec: ERR-004, RPC-020
// TestErrors_MethodNotFound verifies calling an unregistered method returns an error.
func TestErrors_MethodNotFound(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	err := h.Call("workspace.no_such_method", nil, nil)
	if err == nil {
		t.Fatal("unknown method: expected error, got nil")
	}
}

// Spec: ERR-011
// TestErrors_PTYCreateMissingWorkspace verifies pty.create with unknown workspace returns an error.
func TestErrors_PTYCreateMissingWorkspace(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	err := h.Call("pty.create", map[string]any{
		"workspaceId": "ws-does-not-exist",
		"name":        "test",
		"cols":        80,
		"rows":        24,
	}, nil)
	if err == nil {
		t.Fatal("pty.create unknown workspace: expected error, got nil")
	}
}

// Spec: ERR-010, INV-002, INV-018
// TestErrors_DuplicateWorkspaceName verifies workspace.create rejects duplicate names.
func TestErrors_DuplicateWorkspaceName(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	repoPath := harness.MakeLocalGitRepo(t, "dup-name")

	var res struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "dup-test"},
	}, &res)
	id := res.Workspace.ID
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": id}, nil) })

	// Second create with same name must fail.
	err := h.Call("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "dup-test"},
	}, nil)
	if err == nil {
		t.Fatal("create duplicate name: expected error, got nil")
	}
}

// Spec: INV-012, INV-013, WS-026, WS-027, WS-028
// TestErrors_InvalidStateTransitions verifies illegal workspace state transitions are rejected.
func TestErrors_InvalidStateTransitions(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := suite.Harness().ForTest(t)
	repoPath := harness.MakeLocalGitRepo(t, "invalid-sm")

	var res struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "invalid-sm-test"},
	}, &res)
	id := res.Workspace.ID
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": id}, nil) })

	// WS-026: start on created workspace is legal. Start on running is
	// idempotent (returns current state without error).
	h.MustCall("workspace.start", map[string]any{"id": id}, nil)
	harness.WaitForWorkspaceReady(t, h, id)
	err := h.Call("workspace.start", map[string]any{"id": id}, nil)
	if err != nil {
		t.Errorf("start on running workspace: expected nil (idempotent), got %v", err)
	}

	// WS-027: stop on not-running workspace.
	h.MustCall("workspace.stop", map[string]any{"id": id}, nil)
	err = h.Call("workspace.stop", map[string]any{"id": id}, nil)
	if err == nil {
		t.Error("stop on stopped workspace: expected error, got nil")
	}

	// WS-028: remove on running workspace.
	h.MustCall("workspace.start", map[string]any{"id": id}, nil)
	harness.WaitForWorkspaceReady(t, h, id)
	err = h.Call("workspace.remove", map[string]any{"id": id}, nil)
	if err == nil {
		t.Error("remove on running workspace: expected error, got nil")
	}
}

// Spec: INV-014
// TestErrors_RemoveAlreadyRemoved verifies removing an already-removed workspace returns not-found.
func TestErrors_RemoveAlreadyRemoved(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	repoPath := harness.MakeLocalGitRepo(t, "remove-twice")

	var res struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "remove-twice-test"},
	}, &res)
	id := res.Workspace.ID

	h.MustCall("workspace.remove", map[string]any{"id": id}, nil)

	err := h.Call("workspace.remove", map[string]any{"id": id}, nil)
	if err == nil {
		t.Fatal("remove already removed: expected error, got nil")
	}
}
