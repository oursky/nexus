//go:build e2e

package cli_test

import (
	"strings"
	"testing"

	"github.com/inizio/nexus/packages/nexus/test/e2e/harness"
)

// TestCLI_WorkspaceCreate_EndToEnd runs the full production path: project discovery,
// embedded Mutagen sync to the SSH profile host, then workspace.create — same as developers.
func TestCLI_WorkspaceCreate_EndToEnd(t *testing.T) {
	t.Parallel()
	harness.RequireE2EFullStack(t)
	h := harness.NewCLIHarness(t)
	repo := harness.MakeLocalGitRepo(t, "ws-create-e2e")

	out, err := h.Run(t, repo, "workspace", "create", ".")
	if err != nil {
		t.Fatalf("workspace create: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "syncing") || !strings.Contains(s, "synced:") {
		t.Fatalf("expected mutagen sync lines in output: %s", out)
	}
	if !strings.Contains(s, "created workspace") {
		t.Fatalf("expected workspace created: %s", out)
	}
}
