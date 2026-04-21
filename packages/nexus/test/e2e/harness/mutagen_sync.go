//go:build e2e

package harness

import (
	"os"
	"testing"
)

// RequireE2EFullStack fails on CI if SSH profile mode is not enabled; otherwise skips.
// Real workspace flows require loopback SSH + default profile (same as production), not NEXUS_E2E_DAEMON_WEBSOCKET.
func RequireE2EFullStack(t *testing.T) {
	t.Helper()
	if os.Getenv("CI") == "true" && !E2EUseRemoteProfile() {
		t.Fatal("CI e2e requires NEXUS_E2E_REMOTE_PROFILE=1 — run scripts/ci/e2e-ssh-bootstrap.sh before tests")
	}
	if !E2EUseRemoteProfile() {
		t.Skip("full-stack e2e: export NEXUS_E2E_REMOTE_PROFILE=1 and run scripts/ci/e2e-ssh-bootstrap.sh (Linux) or configure loopback SSH + profile")
	}
}

// MirrorProfileConfigHome returns a temp config dir.
// Previously this required SSH/Mutagen; now the daemon accesses the local
// filesystem directly so no mirror or profile config is needed.
func MirrorProfileConfigHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// MirrorGitCheckoutToDaemon registers a project and returns the local checkout
// path as the daemon-side repo path. The daemon runs on the same host in all CI
// and local environments so no file-sync (Mutagen) step is required.
func MirrorGitCheckoutToDaemon(t *testing.T, h *Harness, _ string, clientCheckout, projectName string) (projectID, remotePath string) {
	t.Helper()

	var createRes struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	h.MustCall("project.create", map[string]any{
		"name": projectName,
	}, &createRes)
	projectID = createRes.Project.ID
	if projectID == "" {
		t.Fatal("project.create: empty id")
	}

	return projectID, clientCheckout
}

// MirrorGitToDaemon is MirrorGitCheckoutToDaemon using a CLI harness profile.
func (c *CLIHarness) MirrorGitToDaemon(t *testing.T, clientCheckout, projectName string) (projectID, remotePath string) {
	t.Helper()
	RequireE2EFullStack(t)
	ch := c.ConfigHomeForCLI(t)
	return MirrorGitCheckoutToDaemon(t, c.Harness, ch, clientCheckout, projectName)
}
