//go:build e2e

package tui_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

func TestTUI_LaunchQuit(t *testing.T) {
	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})

	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	time.Sleep(600 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatalf("write q: %v", err)
	}

	waitErr := cmd.Wait()
	close(stopDrain)
	if waitErr != nil {
		t.Fatalf("tui exit: %v\noutput:\n%s", waitErr, stripANSI(string(captured)))
	}
	out := stripANSI(string(captured))
	if !strings.Contains(strings.ToLower(out), "nexus") {
		t.Fatalf("expected Nexus header in output; got:\n%s", truncateOut(out, 2000))
	}
}

func TestTUI_ListShowsWorkspaceAndArrowNav(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-list")

	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	wsName := "tui-e2e-" + time.Now().Format("150405")
	spec := workspace.CreateSpec{
		Repo:          repo,
		Ref:           "main",
		WorkspaceName: wsName,
		AgentProfile:  "default",
		Policy:        workspace.Policy{},
	}
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": spec}, &created); err != nil {
		t.Fatalf("workspace.create: %v", err)
	}

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})

	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	time.Sleep(900 * time.Millisecond)
	idx := workspaceListIndex(t, client, created.Workspace.ID)
	if idx > 0 {
		selectListIndex(ptmx, idx-1)
		time.Sleep(120 * time.Millisecond)
		if _, err := ptmx.Write([]byte("\x1b[B")); err != nil {
			t.Fatalf("arrow down: %v", err)
		}
	} else {
		if _, err := ptmx.Write([]byte("\x1b[B")); err != nil {
			t.Fatalf("arrow down: %v", err)
		}
		time.Sleep(80 * time.Millisecond)
		if _, err := ptmx.Write([]byte("\x1b[A")); err != nil {
			t.Fatalf("arrow up: %v", err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatalf("quit: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("tui exit: %v\n%s", err, stripANSI(string(captured)))
	}
	close(stopDrain)

	out := stripANSI(string(captured))
	if !strings.Contains(out, wsName) && !strings.Contains(out, created.Workspace.ID) {
		t.Fatalf("expected workspace name or id in TUI output; got:\n%s", truncateOut(out, 4000))
	}
}

func TestTUI_ArrowKeysExitCleanly(t *testing.T) {
	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})

	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	time.Sleep(400 * time.Millisecond)
	_, _ = ptmx.Write([]byte("\x1b[A"))
	time.Sleep(100 * time.Millisecond)
	_, _ = ptmx.Write([]byte("\x1b[B"))
	time.Sleep(100 * time.Millisecond)
	_, _ = ptmx.Write([]byte{'q'})
	err = cmd.Wait()
	close(stopDrain)
	if err != nil {
		t.Fatalf("tui exit: %v\n%s", err, stripANSI(string(captured)))
	}
}

func truncateOut(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
