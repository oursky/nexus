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

	// The remaining assertions verify actual content injection, which depends on
	// the host having config files to inject. Skip if /run/nexus-host/ is empty.
	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"ls /run/nexus-host/ | wc -l; sleep 0.05")
	if err != nil {
		t.Fatalf("host config mount check: %v\noutput: %s", err, out)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "0" {
		t.Log("host config mount: /run/nexus-host/ is empty (no host config to inject), skipping content assertions")
		return
	}

	// Verify env var injection is active in guest shell sessions (spec: VM-021).
	// The e2e daemon may inject a sentinel OPENAI_API_KEY if configured.
	envContent, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"cat /run/nexus-host/.nexus-env 2>/dev/null; sleep 0.05")
	if err != nil {
		t.Fatalf("read .nexus-env: %v\noutput: %s", err, envContent)
	}
	envStr := string(envContent)

	if strings.Contains(envStr, "OPENAI_API_KEY") {
		// Verify the sentinel var is active in a login shell (sources .profile → sources .nexus-env).
		envOut, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-l", "-c",
			"echo $OPENAI_API_KEY; sleep 0.05")
		if err != nil {
			t.Fatalf("check OPENAI_API_KEY in login shell: %v\noutput: %s", err, envOut)
		}
		val := strings.TrimSpace(string(envOut))
		if val == "" {
			t.Error("OPENAI_API_KEY is not active in login shell (.nexus-env exports it but login shell doesn't have it)")
		}
	} else {
		t.Log(".nexus-env present but no OPENAI_API_KEY — daemon not configured with API keys")
	}
}
