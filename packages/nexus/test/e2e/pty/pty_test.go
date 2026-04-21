//go:build e2e

package pty_test

import (
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

func TestPTY(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)

	repoPath := harness.MakeLocalGitRepo(t, "pty")
	cfg := harness.MirrorProfileConfigHome(t)
	_, remoteRepo := harness.MirrorGitCheckoutToDaemon(t, h, cfg, repoPath, "proj-pty")

	// Create a workspace first — pty.create requires a workspaceId.
	var createRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          remoteRepo,
			"ref":           "main",
			"workspaceName": "pty-test",
		},
	}, &createRes)
	wsID := createRes.Workspace.ID
	if wsID == "" {
		t.Fatal("workspace.create: got empty workspace id")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": wsID}, nil)
	})

	// Create a PTY session.
	var sessionRes struct {
		ID   string `json:"id"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}
	h.MustCall("pty.create", map[string]any{
		"workspaceId": wsID,
		"name":        "test-session",
		"cols":        80,
		"rows":        24,
	}, &sessionRes)
	sessionID := sessionRes.ID
	if sessionID == "" {
		t.Fatal("pty.create: got empty session id")
	}
	t.Cleanup(func() {
		var closeRes struct {
			Ok bool `json:"ok"`
		}
		if err := h.Call("pty.close", map[string]any{"sessionId": sessionID}, &closeRes); err != nil {
			t.Errorf("pty.close cleanup: %v", err)
			return
		}
		if !closeRes.Ok {
			t.Error("pty.close: expected ok=true")
		}
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
