//go:build e2e

package workspace_test

import (
	"testing"

	"github.com/inizio/nexus/packages/nexus/test/e2e/harness"
)

// TestErrors_WorkspaceNotFound verifies workspace operations on unknown IDs return 404.
func TestErrors_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
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

// TestErrors_CreateMissingRequiredFields verifies workspace.create rejects missing fields.
func TestErrors_CreateMissingRequiredFields(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

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

// TestErrors_MissingIDParam verifies methods that require id reject empty/missing id.
func TestErrors_MissingIDParam(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

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

// TestErrors_SpotlightStopMissingWorkspaceID verifies spotlight.stop with no workspaceId returns 400.
func TestErrors_SpotlightStopMissingWorkspaceID(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	err := h.Call("spotlight.stop", map[string]any{}, nil)
	if err == nil {
		t.Fatal("spotlight.stop with empty workspaceId: expected error, got nil")
	}
}

// TestErrors_SpotlightStopUnknownWorkspace verifies spotlight.stop on a workspace with no
// active forwards succeeds (idempotent — nothing to stop is not an error).
func TestErrors_SpotlightStopUnknownWorkspace(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	err := h.Call("spotlight.stop", map[string]any{"workspaceId": "ws-does-not-exist"}, nil)
	if err != nil {
		t.Fatalf("spotlight.stop on workspace with no forwards: expected nil, got %v", err)
	}
}

// TestErrors_MethodNotFound verifies calling an unregistered method returns an error.
func TestErrors_MethodNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	err := h.Call("workspace.no_such_method", nil, nil)
	if err == nil {
		t.Fatal("unknown method: expected error, got nil")
	}
}

// TestErrors_PTYCreateMissingWorkspace verifies pty.create with unknown workspace returns an error.
func TestErrors_PTYCreateMissingWorkspace(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
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
