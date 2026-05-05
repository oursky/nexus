//go:build e2e

package cli_test

import (
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: CLI-030, CLI-031, CLI-032, CLI-033, CLI-034, CLI-035
// TestCLI_WorkspaceCreate_EndToEnd runs workspace create against a real daemon over the
// SSH profile (same entrypoint developers use).
func TestCLI_WorkspaceCreate_EndToEnd(t *testing.T) {
	t.Parallel()
	harness.RequireE2EFullStack(t)
	h := cliSuite.NewCLIHarness(t)
	repo := harness.MakeLocalGitRepo(t, "ws-create-e2e")

	out, err := h.Run(t, repo, "workspace", "create", ".")
	if err != nil {
		t.Fatalf("workspace create: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "created workspace") {
		t.Fatalf("expected workspace created line; got: %s", out)
	}
}
