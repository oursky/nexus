//go:build e2e

package workspace_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// gitRun runs a git command in dir, failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args[1:], err, out)
	}
}

// createWS creates a workspace with a local repo; does not start it.
func createWS(t *testing.T, h *harness.Harness, repoPath, ref, name string) string {
	t.Helper()
	var res struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": ref, "workspaceName": name},
	}, &res)
	id := res.Workspace.ID
	if id == "" {
		t.Fatalf("create %s: empty id", name)
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": id}, nil) })
	return id
}

// forkWorkspace forks parentID on the given harness, returning the child workspace ID.
func forkWorkspace(t *testing.T, h *harness.Harness, parentID, name, ref string) string {
	t.Helper()
	var res struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id": parentID, "childWorkspaceName": name, "childRef": ref,
	}, &res)
	if !res.Forked {
		t.Fatalf("fork %s: forked=false", name)
	}
	id := res.Workspace.ID
	if id == "" {
		t.Fatalf("fork %s: empty id", name)
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": id}, nil) })
	return id
}

// TestFork_MetadataIntegrity verifies parentWorkspaceId, lineageRootId and childRef
// are correctly set in the fork response and workspace.info.
// Uses a local repo — no Mutagen required.
func TestFork_MetadataIntegrity(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	repoPath := harness.MakeGitRepoWithContent(t, "fork-metadata", map[string]string{
		"hello.txt": "hello world\n",
	})
	gitRun(t, repoPath, "git", "branch", "feature-meta", "main")

	parentID := createWS(t, h, repoPath, "main", "fork-meta-parent")

	var forkRes struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID                string `json:"id"`
			ParentWorkspaceID string `json:"parentWorkspaceId"`
			LineageRootID     string `json:"lineageRootId"`
			Ref               string `json:"ref"`
			WorkspaceName     string `json:"workspaceName"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id":                 parentID,
		"childWorkspaceName": "fork-meta-child",
		"childRef":           "feature-meta",
	}, &forkRes)

	if !forkRes.Forked {
		t.Fatal("fork: expected forked=true")
	}
	childID := forkRes.Workspace.ID
	if childID == "" {
		t.Fatal("fork: empty child id")
	}
	if childID == parentID {
		t.Fatalf("fork: child %q must differ from parent %q", childID, parentID)
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": childID}, nil) })

	if forkRes.Workspace.ParentWorkspaceID != parentID {
		t.Errorf("parentWorkspaceId=%q, want %q", forkRes.Workspace.ParentWorkspaceID, parentID)
	}
	if forkRes.Workspace.LineageRootID != parentID {
		t.Errorf("lineageRootId=%q, want %q (parent is root)", forkRes.Workspace.LineageRootID, parentID)
	}
	if forkRes.Workspace.Ref != "feature-meta" {
		t.Errorf("ref=%q, want feature-meta", forkRes.Workspace.Ref)
	}
	if forkRes.Workspace.WorkspaceName != "fork-meta-child" {
		t.Errorf("workspaceName=%q, want fork-meta-child", forkRes.Workspace.WorkspaceName)
	}

	// Cross-check via workspace.info.
	var infoRes struct {
		Workspace struct {
			ParentWorkspaceID string `json:"parentWorkspaceId"`
			LineageRootID     string `json:"lineageRootId"`
			Ref               string `json:"ref"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.info", map[string]any{"id": childID}, &infoRes)
	if infoRes.Workspace.ParentWorkspaceID != parentID {
		t.Errorf("info: parentWorkspaceId=%q, want %q", infoRes.Workspace.ParentWorkspaceID, parentID)
	}
	if infoRes.Workspace.Ref != "feature-meta" {
		t.Errorf("info: ref=%q, want feature-meta", infoRes.Workspace.Ref)
	}

	// Both parent and child must appear in workspace.list.
	var listRes struct {
		Workspaces []struct {
			ID string `json:"id"`
		} `json:"workspaces"`
	}
	h.MustCall("workspace.list", nil, &listRes)
	var foundParent, foundChild bool
	for _, ws := range listRes.Workspaces {
		switch ws.ID {
		case parentID:
			foundParent = true
		case childID:
			foundChild = true
		}
	}
	if !foundParent {
		t.Errorf("list: parent %s not found after fork", parentID)
	}
	if !foundChild {
		t.Errorf("list: child %s not found after fork", childID)
	}
}

// TestFork_LineageChain verifies nested forks inherit the root's lineageRootId.
func TestFork_LineageChain(t *testing.T) {
	t.Parallel()
	h := suite.Harness().ForTest(t)
	repoPath := harness.MakeLocalGitRepo(t, "fork-lineage")
	gitRun(t, repoPath, "git", "branch", "feature-a", "main")
	gitRun(t, repoPath, "git", "branch", "feature-b", "main")

	rootID := createWS(t, h, repoPath, "main", "lineage-root")
	child1ID := forkWorkspace(t, h, rootID, "lineage-c1", "feature-a")
	child2ID := forkWorkspace(t, h, child1ID, "lineage-c2", "feature-b")

	var c2Info struct {
		Workspace struct {
			ParentWorkspaceID string `json:"parentWorkspaceId"`
			LineageRootID     string `json:"lineageRootId"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.info", map[string]any{"id": child2ID}, &c2Info)
	if c2Info.Workspace.ParentWorkspaceID != child1ID {
		t.Errorf("c2.parentWorkspaceId=%q, want child1 %q", c2Info.Workspace.ParentWorkspaceID, child1ID)
	}
	if c2Info.Workspace.LineageRootID != rootID {
		t.Errorf("c2.lineageRootId=%q, want root %q", c2Info.Workspace.LineageRootID, rootID)
	}
}

// TestFork_ContentVerification verifies workspace exec in parent and child use expected refs.
// With the process backend the child workspace ref is metadata; exec runs in the parent dir.
func TestFork_ContentVerification(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeGitRepoWithContent(t, "fork-content", map[string]string{
		"marker.txt": "parent-content\n",
	})
	gitRun(t, repoPath, "git", "branch", "feature-content", "main")

	var parentRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "fork-cv-parent"},
	}, &parentRes)
	parentID := parentRes.Workspace.ID
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": parentID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": parentID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, parentID)

	var forkRes struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id": parentID, "childWorkspaceName": "fork-cv-child", "childRef": "feature-content",
	}, &forkRes)
	childID := forkRes.Workspace.ID
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": childID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": childID}, nil)

	// Parent ref must remain "main".
	var infoRes struct {
		Workspace struct {
			Ref string `json:"ref"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.info", map[string]any{"id": parentID}, &infoRes)
	if infoRes.Workspace.Ref != "main" {
		t.Errorf("parent ref=%q, want main after fork", infoRes.Workspace.Ref)
	}

	// Child ref must be "feature-content".
	h.MustCall("workspace.info", map[string]any{"id": childID}, &infoRes)
	if infoRes.Workspace.Ref != "feature-content" {
		t.Errorf("child ref=%q, want feature-content", infoRes.Workspace.Ref)
	}

	// Parent exec: marker.txt must be readable.
	out, err := h.Run(t, repoPath, "workspace", "exec", parentID, "--", "cat", "marker.txt")
	if err != nil {
		t.Fatalf("exec parent cat marker.txt: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "parent-content") {
		t.Errorf("parent exec: expected parent-content in output, got %q", string(out))
	}
}

// TestFork_WorktreeSync verifies client-side changes are visible inside the forked workspace
// after Mutagen sync. Requires NEXUS_E2E_REMOTE_PROFILE=1.
func TestFork_WorktreeSync(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	harness.RequireE2EFullStack(t)
	h := harness.NewCLIHarness(t)
	repoPath := harness.MakeGitRepoWithContent(t, "fork-sync", map[string]string{
		"sync_marker.txt": "original-content\n",
	})
	gitRun(t, repoPath, "git", "branch", "feature-sync", "main")

	_, daemonRepo := h.MirrorGitToDaemon(t, repoPath, "proj-fork-sync")

	var parentRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": daemonRepo, "ref": "main", "workspaceName": "fork-sync-parent"},
	}, &parentRes)
	parentID := parentRes.Workspace.ID
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": parentID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": parentID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, parentID)

	var forkRes struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id": parentID, "childWorkspaceName": "fork-sync-child", "childRef": "feature-sync",
	}, &forkRes)
	childID := forkRes.Workspace.ID
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": childID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": childID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, childID)

	out, err := h.Run(t, repoPath, "workspace", "exec", childID, "--", "cat", "sync_marker.txt")
	if err != nil {
		t.Fatalf("workspace exec cat sync_marker.txt: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "original-content") {
		t.Errorf("sync: expected original-content, got %q", string(out))
	}
}
