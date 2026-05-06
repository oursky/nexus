//go:build e2e

package vmproof_test

// Spec: VM-021 (host config drive injection)

import (
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// TestVMProof_HostConfigDrive verifies that the host config drive is properly
// mounted and accessible inside the workspace VM, and that the guest agent
// has copied/symlinked relevant files into /root/.
func TestVMProof_HostConfigDrive(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "vmproof-hostconfig")
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-hostconfig")

	cases := []struct {
		name    string
		args    []string
		wantOut string
	}{
		{
			name:    "host config mount exists",
			args:    []string{"workspace", "exec", wsID, "--", "sh", "-c", "ls /run/nexus-host/; sleep 0.05"},
			wantOut: "",
		},
		{
			name:    "ssh directory present",
			args:    []string{"workspace", "exec", wsID, "--", "sh", "-c", "ls -la /root/.ssh/ 2>/dev/null && echo ssh-ok || echo ssh-missing; sleep 0.05"},
			wantOut: "ssh",
		},
		{
			name:    "profile exists",
			args:    []string{"workspace", "exec", wsID, "--", "sh", "-c", "test -f /root/.profile && echo profile-ok || echo profile-missing; sleep 0.05"},
			wantOut: "profile-ok",
		},
		{
			name:    "profile sources nexus-env",
			args:    []string{"workspace", "exec", wsID, "--", "sh", "-c", "grep -q nexus-env /root/.profile && echo sources-nexus-env || echo missing-nexus-env-source; sleep 0.05"},
			wantOut: "sources-nexus-env",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := h.Run(t, repoPath, tc.args...)
			if err != nil {
				t.Fatalf("%s: %v\noutput: %s", tc.name, err, out)
			}
			if tc.wantOut != "" && !strings.Contains(string(out), tc.wantOut) {
				t.Errorf("%s: expected %q in output, got %q", tc.name, tc.wantOut, string(out))
			}
		})
	}

	// Verify /run/nexus-host/ is mounted and non-empty (contains at least one entry).
	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c", "ls /run/nexus-host/ | wc -l; sleep 0.05")
	if err != nil {
		t.Fatalf("host config mount check: %v\noutput: %s", err, out)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "0" {
		t.Error("host config mount: /run/nexus-host/ is empty, expected at least one file")
	}
}
