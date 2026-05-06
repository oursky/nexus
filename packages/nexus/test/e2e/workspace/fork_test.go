//go:build e2e

package workspace_test

import (
	"os/exec"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: WS-030, WS-031, WS-032, WS-033, WS-059, WS-060, WS-062, INV-010
func TestWorkspaceFork(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)

	clientRepo := harness.MakeLocalGitRepo(t, "fork")
	runGit := func(args ...string) {
		t.Helper()
		c := exec.Command(args[0], args[1:]...)
		c.Dir = clientRepo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("git", "branch", "feature-branch", "main")

	cfg := harness.MirrorProfileConfigHome(t)
	_, daemonRepo := harness.MirrorGitCheckoutToDaemon(t, h, cfg, clientRepo, "proj-fork")

	// 1. Create base workspace
	var createRes struct {
		Workspace struct {
			ID  string `json:"id"`
			Ref string `json:"ref"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          daemonRepo,
			"ref":           "main",
			"workspaceName": "fork-base",
		},
	}, &createRes)
	baseID := createRes.Workspace.ID
	if baseID == "" {
		t.Fatal("create: got empty workspace id")
	}
	t.Cleanup(func() {
		var res struct {
			Removed bool `json:"removed"`
		}
		h.MustCall("workspace.remove", map[string]any{"id": baseID}, &res)
	})

	// 2. Fork the base workspace
	var forkRes struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID                string `json:"id"`
			ParentWorkspaceID string `json:"parentWorkspaceId"`
			LineageRootID     string `json:"lineageRootId"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id":                 baseID,
		"childWorkspaceName": "fork-child",
		"childRef":           "feature-branch",
	}, &forkRes)

	if !forkRes.Forked {
		t.Fatal("fork: expected forked=true")
	}
	forkID := forkRes.Workspace.ID
	if forkID == "" {
		t.Fatal("fork: got empty workspace id")
	}
	t.Cleanup(func() {
		var res struct {
			Removed bool `json:"removed"`
		}
		h.MustCall("workspace.remove", map[string]any{"id": forkID}, &res)
	})

	// 3. Verify fork has a different ID
	if forkID == baseID {
		t.Fatalf("fork: expected different id from base %q, got same", baseID)
	}

	// 4. Verify fork references the parent
	if forkRes.Workspace.ParentWorkspaceID != baseID {
		t.Fatalf("fork: expected parentWorkspaceId=%q, got %q", baseID, forkRes.Workspace.ParentWorkspaceID)
	}

	// 5. Verify via workspace.info
	var infoRes struct {
		Workspace struct {
			ID                string `json:"id"`
			ParentWorkspaceID string `json:"parentWorkspaceId"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.info", map[string]any{"id": forkID}, &infoRes)
	if infoRes.Workspace.ParentWorkspaceID != baseID {
		t.Fatalf("info: fork parentWorkspaceId=%q, want %q", infoRes.Workspace.ParentWorkspaceID, baseID)
	}
}
