//go:build e2e

package vmproof_test

import (
	"strings"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// waitGuestDockerReachable polls the guest until docker info reports a running
// daemon (libkrun starts dockerd asynchronously; a fixed sleep is flaky on CI).
func waitGuestDockerReachable(t *testing.T, h *harness.CLIHarness, repoPath, wsID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
			"docker info 2>&1; true")
		s := string(out)
		if err == nil && strings.Contains(s, "Server Version") &&
			!strings.Contains(s, "Cannot connect to the Docker daemon") {
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
	t.Fatal("timeout waiting for guest Docker daemon")
}

// Spec: VM-PROOF-014
// TestVMProof_DockerDaemon verifies the Docker daemon starts inside the guest VM
// and can execute container commands.
func TestVMProof_DockerDaemon(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "vmproof-docker")
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-docker")

	waitGuestDockerReachable(t, h, repoPath, wsID)

	cases := []struct {
		name    string
		args    []string
		wantOut string
	}{
		{
			name:    "docker info",
			args:    []string{"workspace", "exec", wsID, "--", "sh", "-c", "docker info; sleep 0.1"},
			wantOut: "Server Version",
		},
		{
			name:    "docker run hello-world",
			args:    []string{"workspace", "exec", wsID, "--", "sh", "-c", "docker run --rm hello-world; sleep 0.1"},
			wantOut: "Hello from Docker",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := h.Run(t, repoPath, tc.args...)
			if err != nil {
				t.Fatalf("%s: %v\noutput: %s", tc.name, err, out)
			}
			if !strings.Contains(string(out), tc.wantOut) {
				t.Errorf("%s: expected %q in output, got %q", tc.name, tc.wantOut, string(out))
			}
		})
	}
}
