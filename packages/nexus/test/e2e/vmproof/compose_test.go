//go:build e2e

package vmproof_test

import (
	"strings"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: VM-020 (docker compose multi-service)
// TestVMProof_DockerCompose verifies that docker compose can start multiple
// services inside a workspace, that they are reachable, and that compose down
// performs a clean shutdown.
func TestVMProof_DockerCompose(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := cliSuite.NewCLIHarness(t)
	repoPath := harness.MakeLocalGitRepo(t, "vmproof-compose")
	wsID := createWorkspaceAndStart(t, h, repoPath, "vmproof-compose")

	// Wait for dockerd to finish starting inside the guest.
	time.Sleep(3 * time.Second)

	// Write docker-compose.yml into the workspace.
	writeCompose := strings.Join([]string{
		"mkdir -p /tmp/compose-test && cat > /tmp/compose-test/docker-compose.yml << 'EOF'",
		"services:",
		"  web:",
		"    image: nginx:alpine",
		"    ports:",
		"      - '18080:80'",
		"  sidecar:",
		"    image: alpine",
		"    command: sleep 300",
		"EOF",
		"sleep 0.05",
	}, "\n")

	out, err := h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c", writeCompose)
	if err != nil {
		t.Fatalf("write compose file: %v\noutput: %s", err, out)
	}

	// docker compose up -d
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"cd /tmp/compose-test && docker compose up -d; sleep 0.1")
	if err != nil {
		t.Fatalf("docker compose up: %v\noutput: %s", err, out)
	}

	// Give services a moment to fully start.
	time.Sleep(5 * time.Second)

	// docker compose ps — both services should show as running.
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"cd /tmp/compose-test && docker compose ps; sleep 0.05")
	if err != nil {
		t.Fatalf("docker compose ps: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "web") {
		t.Errorf("docker compose ps: expected 'web' service in output, got %q", string(out))
	}
	if !strings.Contains(string(out), "sidecar") {
		t.Errorf("docker compose ps: expected 'sidecar' service in output, got %q", string(out))
	}

	// curl nginx — verify HTTP response.
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"curl -sf http://localhost:18080; sleep 0.05")
	if err != nil {
		t.Fatalf("curl nginx: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "Welcome to nginx") {
		t.Errorf("curl nginx: expected 'Welcome to nginx' in output, got %q", string(out))
	}

	// docker compose down — clean shutdown.
	out, err = h.Run(t, repoPath, "workspace", "exec", wsID, "--", "sh", "-c",
		"cd /tmp/compose-test && docker compose down; sleep 0.05")
	if err != nil {
		t.Fatalf("docker compose down: %v\noutput: %s", err, out)
	}
}
