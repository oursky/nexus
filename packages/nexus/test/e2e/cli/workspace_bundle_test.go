//go:build e2e

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: VM-009, VM-011, VM-PROOF-007, VM-PROOF-009, CLI-120, CLI-121, CLI-122, CLI-123, CLI-126, CLI-127
func TestCLI_WorkspaceExportImport_EndToEnd(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	clientRepo := harness.MakeLocalGitRepo(t, "ws-bundle-export-import")

	_, daemonRepo := h.MirrorGitToDaemon(t, clientRepo, "proj-ws-bundle-e2e")

	var createRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          daemonRepo,
			"ref":           "main",
			"workspaceName": "bundle-base",
		},
	}, &createRes)
	baseID := createRes.Workspace.ID
	if baseID == "" {
		t.Fatal("workspace.create: empty id")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": baseID}, nil)
	})

	bundlePath := filepath.Join(t.TempDir(), "bundle-base.nxbundle")
	out, err := h.Run(t, clientRepo, "workspace", "export", baseID, "--out", bundlePath)
	if err != nil {
		t.Fatalf("workspace export: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), ".nxbundle") {
		t.Fatalf("workspace export: expected bundle output, got: %s", out)
	}
	if strings.Contains(string(out), "SECRET=") {
		t.Fatalf("workspace export: output leaked raw secret material: %s", out)
	}

	out, err = h.Run(t, clientRepo, "workspace", "import", "--from", bundlePath, "--dry-run")
	if err != nil {
		t.Fatalf("workspace import --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "compatible") {
		t.Fatalf("workspace import --dry-run: expected compatibility output, got: %s", out)
	}
	if !strings.Contains(string(out), "workspace.init") || !strings.Contains(string(out), "workspace.up") || !strings.Contains(string(out), "workspace.down") {
		t.Fatalf("workspace import --dry-run: expected Nexusfile workspace intent fields in diagnostics, got: %s", out)
	}
}

// Spec: VM-010, VM-PROOF-008, CLI-124, CLI-125
func TestCLI_WorkspaceBundleRunner_MacOSCompatibility(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	clientRepo := harness.MakeLocalGitRepo(t, "ws-bundle-runner-compat")

	_, daemonRepo := h.MirrorGitToDaemon(t, clientRepo, "proj-ws-bundle-runner")

	var createRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          daemonRepo,
			"ref":           "main",
			"workspaceName": "bundle-runner-base",
		},
	}, &createRes)
	baseID := createRes.Workspace.ID
	if baseID == "" {
		t.Fatal("workspace.create: empty id")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": baseID}, nil)
	})

	outBase := filepath.Join(t.TempDir(), "bundle-runner")
	out, err := h.Run(t, clientRepo, "workspace", "export", baseID, "--out", outBase)
	if err != nil {
		t.Fatalf("workspace export: %v\n%s", err, out)
	}

	runnerPath := outBase
	if _, statErr := os.Stat(runnerPath); statErr != nil {
		t.Fatalf("standalone runner artifact must be emitted: %v", statErr)
	}

	runnerHelp, err := exec.Command(runnerPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("runner --help: %v\n%s", err, runnerHelp)
	}
	runnerText := string(runnerHelp)
	if !strings.Contains(runnerText, "run") || !strings.Contains(runnerText, "start") || !strings.Contains(runnerText, "exec") || !strings.Contains(runnerText, "stop") {
		t.Fatalf("runner help missing required subcommands, got: %s", runnerHelp)
	}
}

// Spec: VM-012, VM-PROOF-010, CLI-128, CLI-129, CLI-130, CLI-131
func TestCLI_WorkspaceBundleRunner_IntentExecution(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	clientRepo := harness.MakeLocalGitRepo(t, "ws-bundle-runner-intent")

	_, daemonRepo := h.MirrorGitToDaemon(t, clientRepo, "proj-ws-bundle-runner-intent")

	var createRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          daemonRepo,
			"ref":           "main",
			"workspaceName": "bundle-runner-intent",
		},
	}, &createRes)
	baseID := createRes.Workspace.ID
	if baseID == "" {
		t.Fatal("workspace.create: empty id")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": baseID}, nil)
	})

	outBase := filepath.Join(t.TempDir(), "bundle-runner-intent")
	out, err := h.Run(t, clientRepo, "workspace", "export", baseID, "--out", outBase)
	if err != nil {
		t.Fatalf("workspace export: %v\n%s", err, out)
	}

	runnerPath := outBase
	info, statErr := os.Stat(runnerPath)
	if statErr != nil {
		t.Fatalf("standalone runner artifact must be emitted: %v", statErr)
	}
	// CLI-129: runner must be executable.
	if info.Mode()&0o111 == 0 {
		t.Fatalf("runner must be executable, got mode: %v", info.Mode())
	}

	// CLI-128: runner --help must enumerate start/stop/exec/run subcommands.
	helpOut, helpErr := exec.Command(runnerPath, "--help").CombinedOutput()
	if helpErr != nil {
		t.Fatalf("runner --help: %v\n%s", helpErr, helpOut)
	}
	for _, sub := range []string{"start", "stop", "exec", "run"} {
		if !strings.Contains(string(helpOut), sub) {
			t.Fatalf("runner --help missing subcommand %q, got: %s", sub, helpOut)
		}
	}
	// CLI-130: runner help must note services[].start is NOT executed.
	if !strings.Contains(string(helpOut), "services[].start") && !strings.Contains(string(helpOut), "deploy") {
		t.Fatalf("runner --help must document that services[].start is not executed, got: %s", helpOut)
	}

	// CLI-131: first 'start' must run init then up (init marker not present yet).
	stateDir := filepath.Join(t.TempDir(), "nexus-runner-state")
	env := append(os.Environ(), "NEXUS_RUNNER_STATE_DIR="+stateDir)
	cmd := exec.Command(runnerPath, "start")
	cmd.Env = env
	startOut1, startErr1 := cmd.CombinedOutput()
	if startErr1 != nil {
		t.Fatalf("runner start (first): %v\n%s", startErr1, startOut1)
	}
	// Init must have produced a marker file.
	initMarker := filepath.Join(stateDir, ".init-done")
	if _, markerErr := os.Stat(initMarker); markerErr != nil {
		t.Fatalf("runner start (first): init marker %q must exist after first start: %v", initMarker, markerErr)
	}

	// CLI-131: second 'start' must skip init (marker already present).
	cmd2 := exec.Command(runnerPath, "start")
	cmd2.Env = env
	startOut2, startErr2 := cmd2.CombinedOutput()
	if startErr2 != nil {
		t.Fatalf("runner start (second): %v\n%s", startErr2, startOut2)
	}
	if !strings.Contains(string(startOut2), "already completed") {
		t.Fatalf("runner start (second): expected 'already completed' message for skipped init, got: %s", startOut2)
	}

	// CLI-129: 'stop' must execute workspace.down intent (no error even when no-op).
	cmd3 := exec.Command(runnerPath, "stop")
	cmd3.Env = env
	stopOut, stopErr := cmd3.CombinedOutput()
	if stopErr != nil {
		t.Fatalf("runner stop: %v\n%s", stopErr, stopOut)
	}

	// CLI-129: 'exec' must pass through to the real command.
	cmd4 := exec.Command(runnerPath, "exec", "echo", "nexus-runner-exec-ok")
	cmd4.Env = env
	execOut, execErr := cmd4.CombinedOutput()
	if execErr != nil {
		t.Fatalf("runner exec: %v\n%s", execErr, execOut)
	}
	if !strings.Contains(string(execOut), "nexus-runner-exec-ok") {
		t.Fatalf("runner exec: expected passthrough output, got: %s", execOut)
	}

	// Unknown command must exit 2.
	cmd5 := exec.Command(runnerPath, "bogus-command-xyz")
	cmd5.Env = env
	_, unknownErr := cmd5.CombinedOutput()
	if unknownErr == nil {
		t.Fatal("runner unknown command: expected non-zero exit, got success")
	}
	if exitErr, ok := unknownErr.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 2 {
			t.Fatalf("runner unknown command: expected exit 2, got: %d", exitErr.ExitCode())
		}
	}
}

// Intentionally no feature-gating helper: these tests are red tests that codify
// the required export/import/runner behavior before implementation.
