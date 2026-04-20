//go:build e2e

package cli_test

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/inizio/nexus/packages/nexus/test/e2e/harness"
)

// TestCLI_WorkspaceShellPTY drives `nexus workspace shell` through a real pseudoterminal
// (not only stdin pipe), so the interactive code path is exercised.
func TestCLI_WorkspaceShellPTY(t *testing.T) {
	t.Parallel()
	h := harness.NewCLIHarness(t)
	clientRepo := harness.MakeLocalGitRepo(t, "pty-interactive")
	_, daemonRepo := h.MirrorGitToDaemon(t, clientRepo, "proj-pty-interactive")

	var createRes struct {
		Workspace struct {
			ID            string `json:"id"`
			WorkspaceName string `json:"workspaceName"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          daemonRepo,
			"ref":           "main",
			"workspaceName": "pty-interactive",
		},
	}, &createRes)
	wsID := createRes.Workspace.ID
	if wsID == "" {
		t.Fatal("workspace.create: empty id")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": wsID}, nil)
	})

	_, err := h.Run(t, clientRepo, "workspace", "start", wsID)
	if err != nil {
		t.Fatalf("workspace start: %v", err)
	}

	cmd := exec.Command(h.BinPath(), "workspace", "shell", "pty-interactive", "--workdir", "/workspace")
	cmd.Dir = clientRepo
	cmd.Env = h.SubprocessEnv(t)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 120})
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	var output bytes.Buffer
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, ptmx)
		close(copyDone)
	}()

	go func() {
		time.Sleep(400 * time.Millisecond)
		_, _ = ptmx.Write([]byte("echo NEXUS_PTY_INTERACTIVE_OK\n"))
		time.Sleep(100 * time.Millisecond)
		_, _ = ptmx.Write([]byte("exit\n"))
	}()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("workspace shell wait: %v", err)
	}

	select {
	case <-copyDone:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for pty output drain")
	}

	s := output.String()
	if !strings.Contains(s, "NEXUS_PTY_INTERACTIVE_OK") {
		t.Fatalf("expected marker in pty output, got: %q", s)
	}
}
