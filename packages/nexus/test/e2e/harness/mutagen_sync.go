//go:build e2e

package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/mirror"
)

// RequireE2EFullStack fails on CI if Mutagen+SSH profile mode is not enabled; otherwise skips.
// Real workspace flows require loopback SSH + default profile (same as production), not NEXUS_E2E_DAEMON_WEBSOCKET.
func RequireE2EFullStack(t *testing.T) {
	t.Helper()
	if os.Getenv("CI") == "true" && !E2EUseRemoteProfile() {
		t.Fatal("CI e2e requires NEXUS_E2E_REMOTE_PROFILE=1 — run scripts/ci/e2e-ssh-bootstrap.sh before tests")
	}
	if !E2EUseRemoteProfile() {
		t.Skip("full-stack e2e (Mutagen mirror): export NEXUS_E2E_REMOTE_PROFILE=1 and run scripts/ci/e2e-ssh-bootstrap.sh (Linux) or configure loopback SSH + profile")
	}
}

// MirrorProfileConfigHome writes a default profile for Mutagen (SSH to loopback). Daemon WebSocket
// fields are placeholders when only RPC + mirror run (unix socket daemon).
func MirrorProfileConfigHome(t *testing.T) string {
	t.Helper()
	RequireE2EFullStack(t)
	ch := t.TempDir()
	writeE2EProfile(t, ch, sshHostFromEnv(), 7777, "e2e-mirror-placeholder", sshPortFromEnv())
	return ch
}

// MirrorGitCheckoutToDaemon runs project.create + mirror.Ensure (embedded Mutagen) exactly like
// `nexus workspace create`: local checkout → SSH host ~/.local/share/nexus/mirrors/<slug>/.
// Returns project id and the absolute remote path to pass to workspace.create as spec.repo.
//
// configHome is accepted for API compatibility but is no longer used internally; SSH
// coordinates are read from the environment directly so this function is safe to call
// from parallel tests (no t.Setenv).
func MirrorGitCheckoutToDaemon(t *testing.T, h *Harness, _ string, clientCheckout, projectName string) (projectID, remotePath string) {
	t.Helper()
	clientCheckout, err := filepath.Abs(clientCheckout)
	if err != nil {
		t.Fatalf("mirror: abs client path: %v", err)
	}

	var createRes struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	h.MustCall("project.create", map[string]any{
		"name":     projectName,
		"rootPath": clientCheckout,
	}, &createRes)
	projectID = createRes.Project.ID
	if projectID == "" {
		t.Fatal("project.create: empty id")
	}

	// Use env-based SSH coordinates directly — avoids profile.LoadDefault() which
	// would require mutating XDG_CONFIG_HOME via t.Setenv, breaking t.Parallel().
	sshTarget := sshHostFromEnv()
	sshPort := sshPortFromEnv()

	res, err := mirror.Ensure(mirror.Spec{
		LocalPath: clientCheckout,
		ProjectID: projectID,
		SSHTarget: sshTarget,
		SSHPort:   sshPort,
	})
	if err != nil {
		t.Fatalf("mutagen mirror.Ensure: %v", err)
	}
	remotePath = res.RemotePath
	if remotePath == "" {
		t.Fatal("mirror: empty remote path")
	}
	if strings.EqualFold(filepath.Clean(remotePath), filepath.Clean(clientCheckout)) {
		t.Fatalf("mirror: remote path must differ from client checkout (got %q)", remotePath)
	}

	t.Cleanup(func() {
		_ = mirror.Stop(mirror.Spec{
			LocalPath: clientCheckout,
			ProjectID: projectID,
			SSHTarget: sshTarget,
			SSHPort:   sshPort,
		})
	})

	t.Logf("e2e: mutagen synced %q -> remote %q", clientCheckout, remotePath)
	return projectID, remotePath
}

// MirrorGitToDaemon is MirrorGitCheckoutToDaemon using a CLI harness profile (matches subprocess CLI + same daemon token).
func (c *CLIHarness) MirrorGitToDaemon(t *testing.T, clientCheckout, projectName string) (projectID, remotePath string) {
	t.Helper()
	RequireE2EFullStack(t)
	ch := c.ConfigHomeForCLI(t)
	return MirrorGitCheckoutToDaemon(t, c.Harness, ch, clientCheckout, projectName)
}
