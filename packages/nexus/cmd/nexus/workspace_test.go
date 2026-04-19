package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestPrintWorkspaceUsageIncludesStart(t *testing.T) {
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stderr = w

	printWorkspaceUsage()

	_ = w.Close()
	os.Stderr = oldStderr

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read usage output: %v", err)
	}

	got := string(buf)
	if !strings.Contains(got, "start <id>") {
		t.Fatalf("expected usage to include start <id>, got %q", got)
	}
}

func TestRunWorkspaceStartCommandCallsWorkspaceStartRPC(t *testing.T) {
	origEnsure := ensureDaemonFn
	origRPC := daemonRPCFn
	t.Cleanup(func() {
		ensureDaemonFn = origEnsure
		daemonRPCFn = origRPC
	})

	calledMethod := ""
	calledID := ""

	ensureDaemonFn = func() (*websocket.Conn, error) {
		return nil, nil
	}
	daemonRPCFn = func(_ *websocket.Conn, method string, params interface{}, out interface{}) error {
		calledMethod = method
		payload, ok := params.(map[string]any)
		if !ok {
			t.Fatalf("expected map params, got %T", params)
		}
		calledID, _ = payload["id"].(string)
		return nil
	}

	runWorkspaceStartCommand([]string{"ws-123"})

	if calledMethod != "workspace.start" {
		t.Fatalf("expected workspace.start method, got %q", calledMethod)
	}
	if calledID != "ws-123" {
		t.Fatalf("expected workspace id ws-123, got %q", calledID)
	}
}

func TestPrintWorkspaceUsageContainsNewSubcommands(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	printWorkspaceUsage()

	_ = w.Close()
	os.Stderr = oldStderr

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf)

	for _, want := range []string{"info", "restore", "checkout", "ready", "shell", "run"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected usage to contain %q, got:\n%s", want, got)
		}
	}
}

func runWorkspaceSubprocess(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "NEXUS_SUBPROCESS_TEST=1")

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdout, stderr, exitCode
}

func TestWorkspaceInfoRequiresArg(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runWorkspaceInfoCommand([]string{})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceInfoRequiresArg")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing arg, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus workspace info") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestWorkspaceRestoreRequiresArg(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runWorkspaceRestoreCommand([]string{})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceRestoreRequiresArg")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing arg, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus workspace restore") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestWorkspaceCheckoutRequiresArg(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runWorkspaceCheckoutCommand([]string{})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceCheckoutRequiresArg")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing arg, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus workspace checkout") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestWorkspaceCheckoutRequiresRef(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runWorkspaceCheckoutCommand([]string{"my-workspace"})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceCheckoutRequiresRef")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing --ref, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus workspace checkout") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestWorkspaceReadyRequiresArg(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runWorkspaceReadyCommand([]string{})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceReadyRequiresArg")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing arg, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus workspace ready") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestWorkspaceShellRequiresArg(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runShellCommand([]string{})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceShellRequiresArg")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing arg, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus workspace shell") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestWorkspaceRunRequiresNameArg(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runRunCommand([]string{})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceRunRequiresNameArg")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing name arg, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "nexus workspace run") {
		t.Errorf("expected stderr to include usage hint, got:\n%s", stderr)
	}
}

func TestWorkspaceRunRequiresCommandAfterDash(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runRunCommand([]string{"my-workspace", "--"})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceRunRequiresCommandAfterDash")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing command after --, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "command required") {
		t.Errorf("expected stderr to mention command required, got:\n%s", stderr)
	}
}

func TestWorkspaceRunRequiresDashSeparator(t *testing.T) {
	if os.Getenv("NEXUS_SUBPROCESS_TEST") == "1" {
		runRunCommand([]string{"my-workspace"})
		return
	}
	_, stderr, code := runWorkspaceSubprocess(t, "-test.run=TestWorkspaceRunRequiresDashSeparator")
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing -- separator, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "command required") {
		t.Errorf("expected stderr to mention command required, got:\n%s", stderr)
	}
}
