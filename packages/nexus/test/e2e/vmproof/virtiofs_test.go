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

// VM-017: virtiofs-direct guest write reflects to host
// TestVMProof_VirtiofsHostGuestSync verifies that in virtiofs-direct mode, a
// guest write to /workspace is immediately visible on the host workspace directory.
func TestVMProof_VirtiofsHostGuestSync(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)

	repoPath := harness.MakeGitRepoWithContent(t, "vmproof-virtiofs", map[string]string{
		"existing.txt": "pre-existing\n",
	})
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-virtiofs")

	// Guest writes a new file into /workspace.
	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"echo 'virtiofs-written' > /workspace/virtiofs-proof.txt && sync")
	if err != nil {
		t.Fatalf("guest write: %v\n%s", err, out)
	}

	// Allow virtiofs cache to settle.
	time.Sleep(500 * time.Millisecond)

	// Host must see the file written by the guest.
	hostContent, err := os.ReadFile(filepath.Join(repoPath, "virtiofs-proof.txt"))
	if err != nil {
		t.Fatalf("host read: %v", err)
	}
	if !strings.Contains(string(hostContent), "virtiofs-written") {
		t.Errorf("expected 'virtiofs-written' on host, got %q", string(hostContent))
	}
}
