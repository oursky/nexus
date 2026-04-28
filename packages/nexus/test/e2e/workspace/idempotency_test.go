//go:build e2e

package workspace_test

import (
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: WS-005, WS-006, CLI-092
// TestWorkspaceIdempotency verifies that creating a workspace with the same
// name and repo after removal produces a fresh workspace (not a reuse of the
// old ID).
func TestWorkspaceIdempotency(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "idempotency")

	var createRes struct {
		Workspace struct{ ID string `json:"id"` } `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          repoPath,
			"ref":           "main",
			"workspaceName": "idempotency-test",
		},
	}, &createRes)
	firstID := createRes.Workspace.ID
	if firstID == "" {
		t.Fatal("first create: empty workspace id")
	}

	var removeRes struct{ Removed bool `json:"removed"` }
	h.MustCall("workspace.remove", map[string]any{"id": firstID}, &removeRes)
	if !removeRes.Removed {
		t.Fatal("remove: expected removed=true")
	}

	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          repoPath,
			"ref":           "main",
			"workspaceName": "idempotency-test",
		},
	}, &createRes)
	secondID := createRes.Workspace.ID
	if secondID == "" {
		t.Fatal("second create: empty workspace id")
	}

	if firstID == secondID {
		t.Fatalf("expected new workspace id after remove+create, got same id %q", firstID)
	}

	// Verify only the second workspace is present in the list.
	var listRes struct {
		Workspaces []struct{ ID string `json:"id"` } `json:"workspaces"`
	}
	h.MustCall("workspace.list", nil, &listRes)
	foundFirst := false
	foundSecond := false
	for _, ws := range listRes.Workspaces {
		if ws.ID == firstID {
			foundFirst = true
		}
		if ws.ID == secondID {
			foundSecond = true
		}
	}
	if foundFirst {
		t.Errorf("list: removed workspace %q still present", firstID)
	}
	if !foundSecond {
		t.Errorf("list: recreated workspace %q not found", secondID)
	}
}

// Spec: PRJ-003, PRJ-030, INV-003
// TestProjectRepoDedup verifies that creating a project with the same repo URL
// but different name returns the existing project (repo deduplication).
func TestProjectRepoDedup(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "dedup")

	var createRes struct {
		Project struct{ ID string `json:"id"` } `json:"project"`
	}
	h.MustCall("project.create", map[string]any{
		"name":    "first-name",
		"repoUrl": repoPath,
	}, &createRes)
	firstID := createRes.Project.ID
	if firstID == "" {
		t.Fatal("first create: empty project id")
	}
	t.Cleanup(func() { _ = h.Call("project.remove", map[string]any{"id": firstID}, nil) })

	h.MustCall("project.create", map[string]any{
		"name":    "second-name",
		"repoUrl": repoPath,
	}, &createRes)
	secondID := createRes.Project.ID
	if secondID == "" {
		t.Fatal("second create: empty project id")
	}

	if firstID != secondID {
		t.Fatalf("expected same project id for same repo; got %q and %q", firstID, secondID)
	}
}
