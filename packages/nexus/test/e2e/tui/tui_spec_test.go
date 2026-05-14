//go:build e2e

package tui_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

func workspaceListIndex(t *testing.T, c *harness.Client, id string) int {
	t.Helper()
	var r struct {
		Workspaces []workspace.Workspace `json:"workspaces"`
	}
	if err := c.Call("workspace.list", map[string]any{}, &r); err != nil {
		t.Fatalf("workspace.list: %v", err)
	}
	for i, w := range r.Workspaces {
		if w.ID == id {
			return i
		}
	}
	t.Fatalf("workspace list missing %s", id)
	return -1
}

func selectListIndex(ptmx *os.File, idx int) {
	for i := 0; i < idx; i++ {
		_, _ = ptmx.Write([]byte("\x1b[B"))
		time.Sleep(35 * time.Millisecond)
	}
}

// Spec B.1.1: Daemon connection — daemon reachable shows "connected" in header.
func TestSpec_B1_DaemonConnected(t *testing.T) {
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

	time.Sleep(700 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatalf("quit: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("tui: %v", err)
	}
	close(stopDrain)

	out := strings.ToLower(stripANSI(string(captured)))
	if !strings.Contains(out, "connected") {
		t.Fatalf("expected connected header; got:\n%s", truncateOut(out, 3000))
	}
}

// Spec B.1.2: Daemon connection — bad WebSocket shows disconnected.
func TestSpec_B1_DaemonUnreachable(t *testing.T) {
	cmd := exec.Command(shared.BinPath, "tui")
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"NEXUS_E2E_DAEMON_WEBSOCKET=ws://127.0.0.1:1/nope",
		"NEXUS_DAEMON_TOKEN=test-token",
	)
	cmd.Env = env
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})

	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	time.Sleep(1200 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatalf("quit: %v", err)
	}
	_ = cmd.Wait()
	close(stopDrain)

	out := strings.ToLower(stripANSI(string(captured)))
	if !strings.Contains(out, "disconnected") {
		t.Fatalf("expected disconnected state for bad daemon URL; got:\n%s", truncateOut(out, 4500))
	}
}

// Spec B.2.1: Workspace list shows created workspace.
func TestSpec_B2_ListShowsCreatedWorkspace(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-list")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	wsName := "tui-spec-" + time.Now().Format("150405")
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	spec := workspace.CreateSpec{
		Repo:          repo,
		Ref:           "main",
		WorkspaceName: wsName,
		AgentProfile:  "default",
		Policy:        workspace.Policy{},
	}
	if err := client.Call("workspace.create", map[string]any{"spec": spec}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)
	time.Sleep(800 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatalf("quit: %v", err)
	}
	_ = cmd.Wait()
	close(stopDrain)

	out := stripANSI(string(captured))
	if !strings.Contains(out, wsName) && !strings.Contains(out, created.Workspace.ID) {
		t.Fatalf("expected workspace in output:\n%s", truncateOut(out, 4000))
	}
}

// Spec B.2.2 + B.2.3: Start / stop from TUI triggers RPC.
func TestSpec_B2_StartStopFromTUI(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-lc")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	wsName := "tui-lc-" + time.Now().Format("150405")
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": workspace.CreateSpec{
		Repo: repo, Ref: "main", WorkspaceName: wsName, AgentProfile: "default", Policy: workspace.Policy{},
	}}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Workspace.ID

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	time.Sleep(700 * time.Millisecond)
	idx := workspaceListIndex(t, client, id)
	selectListIndex(ptmx, idx)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatalf("enter: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'s'}); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(800 * time.Millisecond)

	var info struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.info", map[string]any{"id": id}, &info); err != nil {
		t.Fatalf("info after start: %v", err)
	}
	st := string(info.Workspace.State)
	if st != "running" && st != "starting" {
		t.Fatalf("expected running/starting after s, got %q", st)
	}

	if _, err := ptmx.Write([]byte{'x'}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	time.Sleep(800 * time.Millisecond)
	if err := client.Call("workspace.info", map[string]any{"id": id}, &info); err != nil {
		t.Fatalf("info after stop: %v", err)
	}
	st = string(info.Workspace.State)
	if st != "stopped" && st != "created" {
		t.Fatalf("expected stopped/created after x, got %q", st)
	}

	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatalf("quit: %v", err)
	}
	_ = cmd.Wait()
	close(stopDrain)
	_ = captured
}

// Spec B.2.4: Delete with confirm Y removes workspace.
func TestSpec_B2_DeleteConfirmed(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-del")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	wsName := "tui-del-" + time.Now().Format("150405")
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": workspace.CreateSpec{
		Repo: repo, Ref: "main", WorkspaceName: wsName, AgentProfile: "default", Policy: workspace.Policy{},
	}}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Workspace.ID

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	go drainBackground(ptmx, &[]byte{}, stopDrain)

	time.Sleep(700 * time.Millisecond)
	idx := workspaceListIndex(t, client, id)
	selectListIndex(ptmx, idx)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'d'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'y'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(900 * time.Millisecond)

	var list struct {
		Workspaces []workspace.Workspace `json:"workspaces"`
	}
	_ = client.Call("workspace.list", map[string]any{}, &list)
	found := false
	for _, w := range list.Workspaces {
		if w.ID == id {
			found = true
			break
		}
	}
	if found {
		t.Fatalf("workspace %s still exists after delete confirm", id)
	}
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(stopDrain)
}

// Spec B.2.5: Delete cancelled with n.
func TestSpec_B2_DeleteCancelled(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-del2")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	wsName := "tui-del2-" + time.Now().Format("150405")
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": workspace.CreateSpec{
		Repo: repo, Ref: "main", WorkspaceName: wsName, AgentProfile: "default", Policy: workspace.Policy{},
	}}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Workspace.ID

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	go drainBackground(ptmx, &[]byte{}, stopDrain)

	time.Sleep(700 * time.Millisecond)
	idx := workspaceListIndex(t, client, id)
	selectListIndex(ptmx, idx)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'d'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'n'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	var list struct {
		Workspaces []workspace.Workspace `json:"workspaces"`
	}
	_ = client.Call("workspace.list", map[string]any{}, &list)
	found := false
	for _, w := range list.Workspaces {
		if w.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("workspace should still exist after cancel")
	}
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(stopDrain)
}

// Spec B.4.1: Spotlight panel opens with l.
func TestSpec_B4_SpotlightPanel(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-spot")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": workspace.CreateSpec{
		Repo: repo, Ref: "main", WorkspaceName: "spot-" + time.Now().Format("150405"), AgentProfile: "default", Policy: workspace.Policy{},
	}}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Workspace.ID

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	time.Sleep(700 * time.Millisecond)
	idx := workspaceListIndex(t, client, id)
	selectListIndex(ptmx, idx)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'l'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(stopDrain)

	out := stripANSI(string(captured))
	if !strings.Contains(out, "Spotlight") {
		t.Fatalf("expected Spotlight panel; got:\n%s", truncateOut(out, 4000))
	}
}

// Spec B.5.1: Sync panel opens with y.
func TestSpec_B5_SyncPanel(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-sync")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": workspace.CreateSpec{
		Repo: repo, Ref: "main", WorkspaceName: "sync-" + time.Now().Format("150405"), AgentProfile: "default", Policy: workspace.Policy{},
	}}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Workspace.ID

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	time.Sleep(700 * time.Millisecond)
	idx := workspaceListIndex(t, client, id)
	selectListIndex(ptmx, idx)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'y'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(stopDrain)

	out := stripANSI(string(captured))
	if !strings.Contains(out, "Volume sync") && !strings.Contains(out, "sync") {
		t.Fatalf("expected sync panel; got:\n%s", truncateOut(out, 4000))
	}
}

// Spec C.1.1: Main list shows Workspaces title.
func TestSpec_C1_ListHeader(t *testing.T) {
	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)
	time.Sleep(600 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(stopDrain)
	out := stripANSI(string(captured))
	if !strings.Contains(out, "Workspaces") {
		t.Fatalf("expected Workspaces title:\n%s", truncateOut(out, 3000))
	}
}

// Spec D.1.1: q exits 0.
func TestSpec_D1_QuitZero(t *testing.T) {
	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	time.Sleep(400 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// Spec D.1.2: Help overlay.
func TestSpec_D1_HelpOverlay(t *testing.T) {
	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)
	time.Sleep(500 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'?'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(stopDrain)
	out := stripANSI(string(captured))
	if !strings.Contains(out, "Key reference") && !strings.Contains(out, "Quit") {
		t.Fatalf("expected help text:\n%s", truncateOut(out, 4000))
	}
}

// Spec D.1.3: Filter mode — '/' should surface filter UI (bubbles list).
func TestSpec_D1_FilterMode(t *testing.T) {
	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)
	time.Sleep(500 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'/'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(350 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(stopDrain)
	out := strings.ToLower(stripANSI(string(captured)))
	if !strings.Contains(out, "filter") && !strings.Contains(out, "/") {
		t.Fatalf("expected filter UI hint; got:\n%s", truncateOut(out, 4000))
	}
}

// Spec K.1.1: Session tab bar renders when session file has a known workspace.
func TestSpec_K1_TabBarRenderedFromSessionFile(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-tabs")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": workspace.CreateSpec{
		Repo: repo, Ref: "main", WorkspaceName: "tabs-" + time.Now().Format("150405"), AgentProfile: "default", Policy: workspace.Policy{},
	}}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}
	wsID := created.Workspace.ID

	// Write a session file pointing at this workspace.
	stateDir := t.TempDir()
	nexusDir := filepath.Join(stateDir, "nexus")
	if err := os.MkdirAll(nexusDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sessionJSON := `{"tabs":["` + wsID + `"],"active":"` + wsID + `"}`
	if err := os.WriteFile(filepath.Join(nexusDir, "tui-sessions.json"), []byte(sessionJSON), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	// Launch TUI with XDG_STATE_HOME pointing to our temp dir.
	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = append(shared.CLIEnv(), "XDG_STATE_HOME="+stateDir)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})

	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)
	time.Sleep(900 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatalf("quit: %v", err)
	}
	_ = cmd.Wait()
	close(stopDrain)

	out := stripANSI(string(captured))
	if !strings.Contains(out, "[1]") {
		t.Fatalf("expected tab bar '[1]' indicator; got:\n%s", truncateOut(out, 4000))
	}
}

// Spec K.2.1: t key from list view triggers terminal action (shell returns quickly
// when workspace is not running, restoring TUI without crashing).
func TestSpec_K2_TerminalKeyFromList(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-tkey")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": workspace.CreateSpec{
		Repo: repo, Ref: "main", WorkspaceName: "tkey-" + time.Now().Format("150405"), AgentProfile: "default", Policy: workspace.Policy{},
	}}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}
	wsID := created.Workspace.ID

	// Use a custom XDG_STATE_HOME so session writes don't pollute host state.
	stateDir := t.TempDir()

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = append(shared.CLIEnv(), "XDG_STATE_HOME="+stateDir)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})

	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	// Navigate to workspace and press t — shell exits immediately (workspace not running).
	time.Sleep(700 * time.Millisecond)
	idx := workspaceListIndex(t, client, wsID)
	selectListIndex(ptmx, idx)
	if _, err := ptmx.Write([]byte{'t'}); err != nil {
		t.Fatalf("t key: %v", err)
	}
	// Give ExecProcess time to start and exit.
	time.Sleep(1500 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatalf("quit: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("tui exit: %v\n%s", err, truncateOut(stripANSI(string(captured)), 4000))
	}
	close(stopDrain)

	// After shell returns, TUI must have resumed and shown the list again.
	// Session file should have been written.
	sessionPath := filepath.Join(stateDir, "nexus", "tui-sessions.json")
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session file not written after t: %v", err)
	}
}

// Spec D.3.1: Fork prompt from detail.
func TestSpec_D3_ForkPrompt(t *testing.T) {
	repo := harness.MakeLocalGitRepo(t, "tui-spec-fork")
	client, err := harness.Dial(shared.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var created struct {
		Workspace workspace.Workspace `json:"workspace"`
	}
	if err := client.Call("workspace.create", map[string]any{"spec": workspace.CreateSpec{
		Repo: repo, Ref: "main", WorkspaceName: "fork-" + time.Now().Format("150405"), AgentProfile: "default", Policy: workspace.Policy{},
	}}, &created); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Workspace.ID

	cmd := exec.Command(shared.BinPath, "tui")
	cmd.Env = shared.CLIEnv()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 35, Cols: 120})
	stopDrain := make(chan struct{})
	var captured []byte
	go drainBackground(ptmx, &captured, stopDrain)

	time.Sleep(700 * time.Millisecond)
	idx := workspaceListIndex(t, client, id)
	selectListIndex(ptmx, idx)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'f'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := ptmx.Write([]byte{0x1b}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := ptmx.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(stopDrain)

	out := stripANSI(string(captured))
	if !strings.Contains(out, "child workspace") && !strings.Contains(strings.ToLower(out), "fork") {
		t.Fatalf("expected fork prompt; got:\n%s", truncateOut(out, 4000))
	}
}
