//go:build e2e

package vmproof_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: VM-PROOF-011
// TestVMProof_HostGuestSync verifies that host file edits to unmodified files
// become visible inside the running guest VM via the virtiofs lowerdir.
func TestVMProof_HostGuestSync(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)

	repoPath := harness.MakeGitRepoWithContent(t, "vmproof-sync", map[string]string{
		"sync.txt": "original\n",
	})
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-sync")

	// Verify original content is visible in guest.
	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "cat", "/workspace/sync.txt")
	if err != nil {
		t.Fatalf("cat original: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "original") {
		t.Fatalf("expected 'original' in guest, got %q", string(out))
	}

	// Host modifies the file.
	if err := os.WriteFile(filepath.Join(repoPath, "sync.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("host write: %v", err)
	}

	// Allow virtiofs cache to settle.
	time.Sleep(500 * time.Millisecond)

	// Guest should see the modified content via the read-only lowerdir.
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "cat", "/workspace/sync.txt")
	if err != nil {
		t.Fatalf("cat modified: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "modified") {
		t.Errorf("expected 'modified' in guest after host edit, got %q", string(out))
	}
}

// Spec: VM-PROOF-012
// TestVMProof_GuestWriteIsolation verifies that guest writes go into the
// overlayfs upperdir and do NOT propagate back to the host project root.
func TestVMProof_GuestWriteIsolation(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)

	repoPath := harness.MakeGitRepoWithContent(t, "vmproof-isolation", map[string]string{
		"isolate.txt": "host-original\n",
	})
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-isolation")

	// Guest writes a new file into the upperdir (no copy-up of virtiofs-backed
	// files needed; a new file goes directly to the ext4 upperdir).
	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"echo 'guest-new' > /workspace/guest-only.txt")
	if err != nil {
		t.Fatalf("guest write: %v\n%s", err, out)
	}

	// Guest should read back its own new file.
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "cat", "/workspace/guest-only.txt")
	if err != nil {
		t.Fatalf("guest cat: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "guest-new") {
		t.Errorf("expected 'guest-new' in guest, got %q", string(out))
	}

	// Host should NOT see the guest-only file (write isolation — upperdir
	// writes never propagate back through virtiofs to the host project root).
	_, statErr := os.Stat(filepath.Join(repoPath, "guest-only.txt"))
	if statErr == nil {
		t.Errorf("guest-only.txt should not exist on host, but it does")
	}

	// The original virtiofs-backed file must be unchanged on the host.
	hostContent, err := os.ReadFile(filepath.Join(repoPath, "isolate.txt"))
	if err != nil {
		t.Fatalf("host read: %v", err)
	}
	if !strings.Contains(string(hostContent), "host-original") {
		t.Errorf("expected 'host-original' on host, got %q", string(hostContent))
	}
}

// Spec: VM-PROOF-013
// TestVMProof_ForkIsolation verifies fork snapshot semantics: the child inherits
// the parent's upperdir state, but subsequent writes in the child are isolated.
func TestVMProof_ForkIsolation(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)

	repoPath := harness.MakeGitRepoWithContent(t, "vmproof-fork", map[string]string{
		"base.txt": "base\n",
	})

	// Create and start parent workspace.
	var parentRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{"repo": repoPath, "ref": "main", "workspaceName": "fork-parent"},
	}, &parentRes)
	parentID := parentRes.Workspace.ID
	if parentID == "" {
		t.Fatal("create parent: empty id")
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": parentID}, nil) })
	h.MustCall("workspace.start", map[string]any{"id": parentID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, parentID)

	// Parent writes a marker file into its upperdir and syncs to ensure the
	// ext4 journal commits before the VM is stopped during fork.
	out, err := h.Run(t, repoPath, "workspace", "exec", parentID, "--", "sh", "-c",
		"echo 'parent-marker' > /workspace/fork-marker.txt && sync")
	if err != nil {
		t.Fatalf("parent write: %v\n%s", err, out)
	}

	// Verify parent can read it back.
	out, err = h.Run(t, repoPath, "workspace", "exec", parentID, "--", "cat", "/workspace/fork-marker.txt")
	if err != nil {
		t.Fatalf("parent cat: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "parent-marker") {
		t.Fatalf("expected parent-marker in parent, got %q", string(out))
	}

	// Fork the parent workspace.
	var forkRes struct {
		Forked    bool `json:"forked"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.fork", map[string]any{
		"id": parentID, "childWorkspaceName": "fork-child", "childRef": "main",
	}, &forkRes)
	if !forkRes.Forked {
		t.Fatal("fork: expected forked=true")
	}
	childID := forkRes.Workspace.ID
	if childID == "" {
		t.Fatal("fork: empty child id")
	}
	t.Cleanup(func() { _ = h.Call("workspace.remove", map[string]any{"id": childID}, nil) })

	// Start child workspace.
	h.MustCall("workspace.start", map[string]any{"id": childID}, nil)
	harness.WaitForWorkspaceReady(t, h.Harness, childID)

	// Child should see the parent's marker (inherited via upperdir snapshot copy).
	out, err = h.Run(t, repoPath, "workspace", "exec", childID, "--", "cat", "/workspace/fork-marker.txt")
	if err != nil {
		t.Fatalf("child cat parent marker: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "parent-marker") {
		t.Errorf("expected parent-marker in child after fork, got %q", string(out))
	}

	// Child overwrites the marker.
	out, err = h.Run(t, repoPath, "workspace", "exec", childID, "--", "sh", "-c",
		"echo 'child-marker' > /workspace/fork-marker.txt")
	if err != nil {
		t.Fatalf("child write: %v\n%s", err, out)
	}

	// Child should now see its own version.
	out, err = h.Run(t, repoPath, "workspace", "exec", childID, "--", "cat", "/workspace/fork-marker.txt")
	if err != nil {
		t.Fatalf("child cat child marker: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "child-marker") {
		t.Errorf("expected child-marker in child after write, got %q", string(out))
	}

	// Parent should still see its original version (upperdirs are isolated after fork).
	out, err = h.Run(t, repoPath, "workspace", "exec", parentID, "--", "cat", "/workspace/fork-marker.txt")
	if err != nil {
		t.Fatalf("parent cat after child write: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "parent-marker") {
		t.Errorf("expected parent-marker still in parent after child write, got %q", string(out))
	}
	if strings.Contains(string(out), "child-marker") {
		t.Errorf("parent must NOT see child-marker; got %q", string(out))
	}
}
