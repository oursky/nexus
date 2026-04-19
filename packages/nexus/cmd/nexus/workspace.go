package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inizio/nexus/packages/nexus/pkg/daemonclient"
	"github.com/inizio/nexus/packages/nexus/pkg/localws"
	"github.com/inizio/nexus/packages/nexus/pkg/workspacemgr"
)

type preflightErrorEnvelope struct {
	Status         string `json:"status"`
	SetupAttempted bool   `json:"setupAttempted"`
	SetupOutcome   string `json:"setupOutcome"`
	Checks         []struct {
		Name        string `json:"name"`
		OK          bool   `json:"ok"`
		Message     string `json:"message"`
		Remediation string `json:"remediation"`
		Installable bool   `json:"installable,omitempty"`
	} `json:"checks"`
}

func renderPreflightCreateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	idx := strings.Index(msg, "runtime preflight failed:")
	if idx < 0 {
		return false
	}
	jsonStart := strings.Index(msg[idx:], "{")
	if jsonStart < 0 {
		return false
	}
	jsonPayload := strings.TrimSpace(msg[idx+jsonStart:])

	var payload preflightErrorEnvelope
	if unmarshalErr := json.Unmarshal([]byte(jsonPayload), &payload); unmarshalErr != nil {
		return false
	}

	fmt.Fprintln(os.Stderr, "nexus workspace create: runtime preflight failed")
	fmt.Fprintf(os.Stderr, "status: %s\n", payload.Status)
	if payload.SetupAttempted {
		if strings.TrimSpace(payload.SetupOutcome) != "" {
			fmt.Fprintf(os.Stderr, "setup: attempted (%s)\n", payload.SetupOutcome)
		} else {
			fmt.Fprintln(os.Stderr, "setup: attempted")
		}
	}
	for _, check := range payload.Checks {
		if check.OK {
			continue
		}
		suffix := ""
		if check.Installable {
			suffix = " (installable)"
		}
		fmt.Fprintf(os.Stderr, "- %s%s", check.Name, suffix)
		if strings.TrimSpace(check.Message) != "" {
			fmt.Fprintf(os.Stderr, ": %s", check.Message)
		}
		fmt.Fprintln(os.Stderr)
		if strings.TrimSpace(check.Remediation) != "" {
			fmt.Fprintf(os.Stderr, "  remediation: %s\n", check.Remediation)
		}
	}
	return true
}

// ── Daemon connection settings ────────────────────────────────────────────────

const defaultDaemonPort = 7874

func daemonPort() int {
	if v := os.Getenv("NEXUS_DAEMON_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			return p
		}
	}
	return defaultDaemonPort
}

func daemonToken() (string, error) {
	if t := os.Getenv("NEXUS_DAEMON_TOKEN"); t != "" {
		return t, nil
	}
	return daemonclient.LoadOrCreateToken()
}

// ensureDaemon starts the daemon if it is not already running and returns
// an authenticated WebSocket connection to it.
func ensureDaemon() (*websocket.Conn, error) {
	port := daemonPort()
	token, err := daemonToken()
	if err != nil {
		return nil, fmt.Errorf("daemon token: %w", err)
	}

	if err := daemonclient.EnsureRunning(port, token, ""); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}

	url := fmt.Sprintf("ws://localhost:%d/?token=%s", port, token)
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	return conn, nil
}

var ensureDaemonFn = ensureDaemon
var daemonRPCFn = daemonRPC

// ── workspace list ────────────────────────────────────────────────────────────

func runWorkspaceListCommand(_ []string) {
	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace list: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	var result struct {
		Workspaces []workspacemgr.Workspace `json:"workspaces"`
	}
	if err := daemonRPC(conn, "workspace.list", map[string]any{}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace list: %v\n", err)
		os.Exit(1)
	}

	if len(result.Workspaces) == 0 {
		fmt.Println("no workspaces")
		return
	}
	fmt.Printf("%-36s  %-20s  %-10s  %-10s  %s\n", "ID", "NAME", "STATE", "BACKEND", "WORKTREE")
	fmt.Printf("%-36s  %-20s  %-10s  %-10s  %s\n",
		"------------------------------------", "--------------------",
		"----------", "----------", "--------")
	for _, ws := range result.Workspaces {
		wt := ws.LocalWorktreePath
		if wt == "" {
			wt = "—"
		}
		fmt.Printf("%-36s  %-20s  %-10s  %-10s  %s\n",
			ws.ID, ws.WorkspaceName, ws.State, ws.Backend, wt)
	}
}

// ── workspace create ──────────────────────────────────────────────────────────

func runWorkspaceCreateCommand(args []string) {
	fs := flag.NewFlagSet("workspace create", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	repo := fs.String("repo", "", "repository URL (required)")
	ref := fs.String("ref", "", "branch / ref (default: repo default branch)")
	name := fs.String("name", "", "workspace name (required)")
	profile := fs.String("profile", "default", "agent profile")
	backend := fs.String("backend", "", "runtime backend override (firecracker)")
	_ = fs.Parse(args)

	if *repo == "" || *name == "" {
		fmt.Fprintf(os.Stderr, "nexus workspace create: --repo and --name are required\n")
		fs.Usage()
		os.Exit(2)
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace create: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	repoValue := normalizeRepoForCreate(*repo)

	spec := workspacemgr.CreateSpec{
		Repo:          repoValue,
		Ref:           *ref,
		WorkspaceName: *name,
		AgentProfile:  *profile,
		Backend:       strings.TrimSpace(*backend),
	}
	var result struct {
		Workspace workspacemgr.Workspace `json:"workspace"`
	}
	if err := daemonRPC(conn, "workspace.create", map[string]any{"spec": spec}, &result); err != nil {
		if renderPreflightCreateError(err) {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "nexus workspace create: %v\n", err)
		os.Exit(1)
	}

	ws := result.Workspace
	fmt.Printf("created workspace %s  (id: %s)\n", ws.WorkspaceName, ws.ID)

	// ── Set up local worktree + optional mutagen sync ─────────────────────
	// A remote sandbox path is needed for the mutagen sync beta endpoint.
	// If RootPath is empty (the daemon hasn't assigned one yet) we still set
	// up the local worktree; we just skip the sync.
	lwMgr, lwErr := localws.NewManager(localws.Config{})
	if lwErr != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace create: warning: cannot init localws manager: %v\n", lwErr)
	} else {
		setupSpec := localws.SetupSpec{
			WorkspaceID:   ws.ID,
			WorkspaceName: ws.WorkspaceName,
			Repo:          ws.Repo,
			Ref:           ws.Ref,
			RemotePath:    ws.RootPath, // empty → mutagen skipped gracefully
		}
		setupResult, setupErr := lwMgr.Setup(context.Background(), setupSpec)
		if setupErr != nil {
			fmt.Fprintf(os.Stderr, "nexus workspace create: warning: local worktree setup failed: %v\n", setupErr)
		} else {
			// Persist worktree info back on the daemon record.
			setParams := map[string]any{
				"id":                ws.ID,
				"localWorktreePath": setupResult.WorktreePath,
				"mutagenSessionId":  setupResult.MutagenSessionID,
			}
			if rpcErr := daemonRPC(conn, "workspace.setLocalWorktree", setParams, nil); rpcErr != nil {
				fmt.Fprintf(os.Stderr, "nexus workspace create: warning: setLocalWorktree RPC failed: %v\n", rpcErr)
			}
			fmt.Printf("local worktree:   %s\n", setupResult.WorktreePath)
			if setupResult.MutagenSessionID != "" {
				fmt.Printf("mutagen session:  %s\n", setupResult.MutagenSessionID)
			}
		}
	}
}

func normalizeRepoForCreate(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" || looksLikeRemoteRepo(repo) {
		return repo
	}

	if filepath.IsAbs(repo) {
		return filepath.Clean(repo)
	}

	if strings.HasPrefix(repo, "./") || strings.HasPrefix(repo, "../") {
		if abs, err := filepath.Abs(repo); err == nil {
			return abs
		}
		return repo
	}

	if info, err := os.Stat(repo); err == nil && info.IsDir() {
		if abs, absErr := filepath.Abs(repo); absErr == nil {
			return abs
		}
	}

	return repo
}

func looksLikeRemoteRepo(repo string) bool {
	if strings.HasPrefix(repo, "git@") || strings.HasPrefix(repo, "ssh://") {
		return true
	}
	u, err := url.Parse(repo)
	return err == nil && u.Scheme != "" && u.Host != ""
}

// ── workspace stop ────────────────────────────────────────────────────────────

func runWorkspaceStopCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace stop <id>")
		os.Exit(2)
	}
	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace stop: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := daemonRPC(conn, "workspace.stop", map[string]any{"id": args[0]}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace stop: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("stopped workspace %s\n", args[0])
}

// ── workspace start ───────────────────────────────────────────────────────────

func runWorkspaceStartCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace start <id>")
		os.Exit(2)
	}
	conn, err := ensureDaemonFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace start: %v\n", err)
		os.Exit(1)
	}
	if conn != nil {
		defer conn.Close()
	}

	if err := daemonRPCFn(conn, "workspace.start", map[string]any{"id": args[0]}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("started workspace %s\n", args[0])
}

// ── workspace remove ──────────────────────────────────────────────────────────

func runWorkspaceRemoveCommand(args []string) {
	fs := flag.NewFlagSet("workspace remove", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "skip confirmation prompt")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace remove <id> [--force]")
		os.Exit(2)
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace remove: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if !*force {
		fi, _ := os.Stdin.Stat()
		isTTY := (fi.Mode() & os.ModeCharDevice) != 0
		if isTTY {
			if !confirmPrompt(fmt.Sprintf("remove workspace %s?", rest[0])) {
				fmt.Println("aborted")
				return
			}
		}
	}

	if err := daemonRPC(conn, "workspace.remove", map[string]any{"id": rest[0]}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace remove: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("removed workspace %s\n", rest[0])
}

// ── workspace info ────────────────────────────────────────────────────────────

func runWorkspaceInfoCommand(args []string) {
	fs := flag.NewFlagSet("workspace info", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace info <id|name> [--json]")
		os.Exit(2)
	}
	nameOrID := fs.Arg(0)

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace info: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	wsID, err := resolveWorkspaceID(context.Background(), conn, nameOrID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace info: %v\n", err)
		os.Exit(1)
	}

	var result struct {
		Workspace struct {
			ID                string `json:"id"`
			WorkspaceName     string `json:"workspaceName"`
			State             string `json:"state"`
			Repo              string `json:"repo"`
			Ref               string `json:"ref"`
			Backend           string `json:"backend"`
			RootPath          string `json:"rootPath"`
			AgentProfile      string `json:"agentProfile"`
			ParentWorkspaceID string `json:"parentWorkspaceId"`
			LineageRootID     string `json:"lineageRootId"`
			ProjectID         string `json:"projectId"`
		} `json:"workspace"`
	}
	if err := daemonRPC(conn, "workspace.info", map[string]any{"id": wsID}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace info: %v\n", err)
		os.Exit(1)
	}

	if *jsonFlag {
		printJSON(result.Workspace)
		return
	}

	ws := result.Workspace
	fmt.Printf("id:                 %s\n", ws.ID)
	fmt.Printf("name:               %s\n", ws.WorkspaceName)
	fmt.Printf("state:              %s\n", ws.State)
	fmt.Printf("repo:               %s\n", ws.Repo)
	fmt.Printf("ref:                %s\n", ws.Ref)
	fmt.Printf("backend:            %s\n", ws.Backend)
	fmt.Printf("rootPath:           %s\n", ws.RootPath)
	fmt.Printf("agentProfile:       %s\n", ws.AgentProfile)
	if ws.ProjectID != "" {
		fmt.Printf("projectId:          %s\n", ws.ProjectID)
	}
	if ws.ParentWorkspaceID != "" {
		fmt.Printf("parentWorkspaceId:  %s\n", ws.ParentWorkspaceID)
	}
	if ws.LineageRootID != "" {
		fmt.Printf("lineageRootId:      %s\n", ws.LineageRootID)
	}
}

// ── workspace restore ─────────────────────────────────────────────────────────

func runWorkspaceRestoreCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace restore <id|name>")
		os.Exit(2)
	}
	nameOrID := args[0]

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace restore: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	wsID, err := resolveWorkspaceID(context.Background(), conn, nameOrID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace restore: %v\n", err)
		os.Exit(1)
	}

	var result struct {
		Workspace struct {
			WorkspaceName string `json:"workspaceName"`
		} `json:"workspace"`
	}
	if err := daemonRPC(conn, "workspace.restore", map[string]any{"id": wsID}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace restore: %v\n", err)
		os.Exit(1)
	}

	name := result.Workspace.WorkspaceName
	if name == "" {
		name = nameOrID
	}
	fmt.Printf("Restored workspace %s\n", name)
}

// ── workspace checkout ────────────────────────────────────────────────────────

func runWorkspaceCheckoutCommand(args []string) {
	fs := flag.NewFlagSet("workspace checkout", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	ref := fs.String("ref", "", "branch or ref to check out (required)")
	_ = fs.Parse(args)

	if fs.NArg() == 0 || *ref == "" {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace checkout <id|name> --ref <branch>")
		os.Exit(2)
	}
	nameOrID := fs.Arg(0)

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace checkout: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	wsID, err := resolveWorkspaceID(context.Background(), conn, nameOrID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace checkout: %v\n", err)
		os.Exit(1)
	}

	var result struct {
		Workspace struct {
			WorkspaceName string `json:"workspaceName"`
		} `json:"workspace"`
	}
	if err := daemonRPC(conn, "workspace.checkout", map[string]any{
		"id":        wsID,
		"targetRef": *ref,
	}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace checkout: %v\n", err)
		os.Exit(1)
	}

	name := result.Workspace.WorkspaceName
	if name == "" {
		name = nameOrID
	}
	fmt.Printf("Checked out %s in workspace %s\n", *ref, name)
}

// ── workspace ready ───────────────────────────────────────────────────────────

func runWorkspaceReadyCommand(args []string) {
	fs := flag.NewFlagSet("workspace ready", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Duration("timeout", 60*1e9, "timeout duration (e.g. 60s, 2m)")
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace ready <id|name> [--timeout 60s]")
		os.Exit(2)
	}
	nameOrID := fs.Arg(0)

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace ready: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	wsID, err := resolveWorkspaceID(ctx, conn, nameOrID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace ready: %v\n", err)
		os.Exit(1)
	}

	var result struct {
		Ready bool `json:"ready"`
	}
	if err := daemonRPC(conn, "workspace.ready", map[string]any{"id": wsID}, &result); err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "nexus workspace ready: timeout waiting for workspace %s\n", nameOrID)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "nexus workspace ready: %v\n", err)
		os.Exit(1)
	}

	if !result.Ready {
		fmt.Fprintf(os.Stderr, "nexus workspace ready: workspace %s is not ready\n", nameOrID)
		os.Exit(1)
	}

	fmt.Printf("Workspace %s is ready\n", nameOrID)
}

// ── workspace fork ────────────────────────────────────────────────────────────

func runWorkspaceForkCommand(args []string) {
	fs := flag.NewFlagSet("workspace fork", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "source workspace ID (required)")
	childName := fs.String("name", "", "child workspace name (required)")
	_ = fs.Parse(args)

	if *id == "" || *childName == "" {
		fmt.Fprintf(os.Stderr, "nexus workspace fork: --id and --name are required\n")
		fs.Usage()
		os.Exit(2)
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace fork: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	var result struct {
		Workspace workspacemgr.Workspace `json:"workspace"`
	}
	if err := daemonRPC(conn, "workspace.fork", map[string]any{
		"id": *id, "childWorkspaceName": *childName,
	}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace fork: %v\n", err)
		os.Exit(1)
	}

	ws := result.Workspace
	fmt.Printf("forked workspace %s  (id: %s)\n", ws.WorkspaceName, ws.ID)

	if strings.TrimSpace(ws.LocalWorktreePath) != "" {
		fmt.Printf("local worktree:   %s\n", ws.LocalWorktreePath)
	}
}

// ── workspace portal ─────────────────────────────────────────────────────────

func runWorkspacePortalCommand(_ []string) {
	port := daemonPort()
	token, err := daemonToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace portal: %v\n", err)
		os.Exit(1)
	}
	if err := daemonclient.EnsureRunning(port, token, ""); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace portal: %v\n", err)
		os.Exit(1)
	}
	url := fmt.Sprintf("http://localhost:%d/portal", port)
	fmt.Printf("portal: %s\n", url)
	// Attempt to open in browser (best-effort).
	_ = openBrowser(url)
}

// ── workspace shell ───────────────────────────────────────────────────────────

func runShellCommand(args []string) {
	fs := flag.NewFlagSet("workspace shell", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	shellFlag := fs.String("shell", "", "shell to use (default: $SHELL or /bin/bash)")
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace shell <id|name> [--shell /bin/bash]")
		os.Exit(2)
	}
	nameOrID := fs.Arg(0)

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace shell: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	wsID, err := resolveWorkspaceID(context.Background(), conn, nameOrID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace shell: %v\n", err)
		os.Exit(1)
	}

	cols, rows := termSize(os.Stdin.Fd())

	shell := strings.TrimSpace(*shellFlag)
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/bash"
	}

	var openResult struct {
		SessionID string `json:"sessionId"`
	}
	if err := daemonRPC(conn, "pty.open", map[string]any{
		"workspaceId": wsID,
		"shell":       shell,
		"cols":        cols,
		"rows":        rows,
	}, &openResult); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace shell: pty.open: %v\n", err)
		os.Exit(1)
	}
	sessionID := openResult.SessionID

	oldTerm, rawErr := makeRaw(os.Stdin.Fd())
	if rawErr != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace shell: raw mode: %v\n", rawErr)
		_ = daemonRPC(conn, "pty.close", map[string]any{"sessionId": sessionID}, nil)
		os.Exit(1)
	}

	exitCode := 0
	exitCh := make(chan int, 1)

	cleanup := func() {
		restoreTerminal(os.Stdin.Fd(), oldTerm)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cleanup()
		_ = daemonRPC(conn, "pty.close", map[string]any{"sessionId": sessionID}, nil)
		os.Exit(1)
	}()

	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for range winchCh {
			c, r := termSize(os.Stdin.Fd())
			_ = daemonRPC(conn, "pty.resize", map[string]any{
				"sessionId": sessionID,
				"cols":      c,
				"rows":      r,
			}, nil)
		}
	}()

	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				_ = daemonRPC(conn, "pty.write", map[string]any{
					"sessionId": sessionID,
					"data":      string(buf[:n]),
				}, nil)
			}
			if err != nil {
				break
			}
		}
	}()

	type ptyNotification struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      string          `json:"id,omitempty"`
		Params  json.RawMessage `json:"params,omitempty"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   json.RawMessage `json:"error,omitempty"`
	}

	for {
		var msg ptyNotification
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}

		switch msg.Method {
		case "pty.data":
			var p struct {
				SessionID string `json:"sessionId"`
				Data      string `json:"data"`
			}
			if err := json.Unmarshal(msg.Params, &p); err == nil && p.SessionID == sessionID {
				_, _ = os.Stdout.WriteString(p.Data)
			}
		case "pty.exit":
			var p struct {
				SessionID string `json:"sessionId"`
				ExitCode  int    `json:"exitCode"`
			}
			if err := json.Unmarshal(msg.Params, &p); err == nil && p.SessionID == sessionID {
				exitCh <- p.ExitCode
			}
		default:
			if msg.ID != "" {
			}
		}

		select {
		case code := <-exitCh:
			exitCode = code
			goto done
		default:
		}
	}

done:
	cleanup()
	signal.Stop(sigCh)
	signal.Stop(winchCh)
	_ = daemonRPC(conn, "pty.close", map[string]any{"sessionId": sessionID}, nil)
	os.Exit(exitCode)
}

// ── workspace run ─────────────────────────────────────────────────────────────

func runRunCommand(args []string) {
	fs := flag.NewFlagSet("workspace run", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	noCleanup := fs.Bool("no-cleanup", false, "do not remove fork after command exits")
	timeout := fs.Duration("timeout", 0, "timeout for the command (e.g. 60s)")
	_ = fs.Bool("json", false, "reserved for future use")

	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
			break
		}
	}

	var flagArgs, cmdArgs []string
	if dashIdx >= 0 {
		flagArgs = args[:dashIdx]
		cmdArgs = args[dashIdx+1:]
	} else {
		flagArgs = args
	}

	_ = fs.Parse(flagArgs)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus workspace run <id|name> [--no-cleanup] [--timeout 60s] -- <command> [args...]")
		os.Exit(2)
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "nexus workspace run: command required after --")
		os.Exit(2)
	}
	nameOrID := fs.Arg(0)

	shellCmd := strings.Join(cmdArgs, " ")

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace run: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	wsID, err := resolveWorkspaceID(ctx, conn, nameOrID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace run: %v\n", err)
		os.Exit(1)
	}

	forkName := fmt.Sprintf("run-fork-%d", time.Now().UnixNano())
	var forkResult struct {
		Workspace workspacemgr.Workspace `json:"workspace"`
	}
	if err := daemonRPC(conn, "workspace.fork", map[string]any{
		"id":                 wsID,
		"childWorkspaceName": forkName,
	}, &forkResult); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace run: fork: %v\n", err)
		os.Exit(1)
	}
	forkID := forkResult.Workspace.ID

	cleanupFork := func() {
		if !*noCleanup {
			_ = daemonRPC(conn, "workspace.remove", map[string]any{"id": forkID}, nil)
		}
	}

	if err := daemonRPC(conn, "workspace.start", map[string]any{"id": forkID}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace run: start fork: %v\n", err)
		cleanupFork()
		os.Exit(1)
	}

	cols, rows := termSize(os.Stdin.Fd())

	var openResult struct {
		SessionID string `json:"sessionId"`
	}
	if err := daemonRPC(conn, "pty.open", map[string]any{
		"workspaceId": forkID,
		"shell":       "/bin/sh",
		"cols":        cols,
		"rows":        rows,
	}, &openResult); err != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace run: pty.open: %v\n", err)
		cleanupFork()
		os.Exit(1)
	}
	sessionID := openResult.SessionID

	// Write the command to the PTY's stdin immediately; /bin/sh will execute it and exit.
	_ = daemonRPC(conn, "pty.write", map[string]any{
		"sessionId": sessionID,
		"data":      shellCmd + "\n",
	}, nil)

	oldTerm, rawErr := makeRaw(os.Stdin.Fd())
	if rawErr != nil {
		fmt.Fprintf(os.Stderr, "nexus workspace run: raw mode: %v\n", rawErr)
		_ = daemonRPC(conn, "pty.close", map[string]any{"sessionId": sessionID}, nil)
		cleanupFork()
		os.Exit(1)
	}

	restoreTerm := func() {
		restoreTerminal(os.Stdin.Fd(), oldTerm)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		restoreTerm()
		_ = daemonRPC(conn, "pty.close", map[string]any{"sessionId": sessionID}, nil)
		cleanupFork()
		os.Exit(1)
	}()

	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for range winchCh {
			c, r := termSize(os.Stdin.Fd())
			_ = daemonRPC(conn, "pty.resize", map[string]any{
				"sessionId": sessionID,
				"cols":      c,
				"rows":      r,
			}, nil)
		}
	}()

	go func() {
		buf := make([]byte, 256)
		for {
			n, readErr := os.Stdin.Read(buf)
			if n > 0 {
				_ = daemonRPC(conn, "pty.write", map[string]any{
					"sessionId": sessionID,
					"data":      string(buf[:n]),
				}, nil)
			}
			if readErr != nil {
				break
			}
		}
	}()

	exitCode := 0
	exitCh := make(chan int, 1)

	type ptyNotification struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      string          `json:"id,omitempty"`
		Params  json.RawMessage `json:"params,omitempty"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   json.RawMessage `json:"error,omitempty"`
	}

	for {
		var msg ptyNotification
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}

		switch msg.Method {
		case "pty.data":
			var p struct {
				SessionID string `json:"sessionId"`
				Data      string `json:"data"`
			}
			if err := json.Unmarshal(msg.Params, &p); err == nil && p.SessionID == sessionID {
				_, _ = os.Stdout.WriteString(p.Data)
			}
		case "pty.exit":
			var p struct {
				SessionID string `json:"sessionId"`
				ExitCode  int    `json:"exitCode"`
			}
			if err := json.Unmarshal(msg.Params, &p); err == nil && p.SessionID == sessionID {
				exitCh <- p.ExitCode
			}
		}

		select {
		case code := <-exitCh:
			exitCode = code
			goto runDone
		default:
		}
	}

runDone:
	restoreTerm()
	signal.Stop(sigCh)
	signal.Stop(winchCh)
	_ = daemonRPC(conn, "pty.close", map[string]any{"sessionId": sessionID}, nil)
	cleanupFork()
	os.Exit(exitCode)
}

// ── top-level workspace dispatcher ───────────────────────────────────────────

// runWorkspaceCommand dispatches nexus workspace <sub> args.
func runWorkspaceCommand(args []string) {
	if len(args) == 0 {
		printWorkspaceUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		runWorkspaceListCommand(rest)
	case "create":
		runWorkspaceCreateCommand(rest)
	case "start":
		runWorkspaceStartCommand(rest)
	case "stop":
		runWorkspaceStopCommand(rest)
	case "remove", "rm", "delete":
		runWorkspaceRemoveCommand(rest)
	case "fork":
		runWorkspaceForkCommand(rest)
	case "portal":
		runWorkspacePortalCommand(rest)
	case "shell":
		runShellCommand(rest)
	case "run":
		runRunCommand(rest)
	case "info":
		runWorkspaceInfoCommand(rest)
	case "restore":
		runWorkspaceRestoreCommand(rest)
	case "checkout":
		runWorkspaceCheckoutCommand(rest)
	case "ready":
		runWorkspaceReadyCommand(rest)
	default:
		printWorkspaceUsage()
		fmt.Fprintf(os.Stderr, "\nunknown workspace subcommand: %s\n", sub)
		os.Exit(2)
	}
}

func printWorkspaceUsage() {
	fmt.Fprint(os.Stderr, `usage: nexus workspace <subcommand> [options]

subcommands:
  list                  list all workspaces
  create --repo <url|path> --name <name> [--ref <ref>] [--profile <profile>] [--backend <backend>]
  info <id|name> [--json]
                        show workspace details
  start <id>            start a workspace and make it accessible
  stop <id>             stop a running workspace
  restore <id|name>     restore a stopped or errored workspace
  checkout <id|name> --ref <branch>
                        check out a branch in a workspace
  ready <id|name> [--timeout 60s]
                        wait until the workspace is ready
  remove <id> [--force]  remove a workspace
  fork --id <id> --name <child-name>
  portal                open the admin portal in your browser
  shell <id|name> [--shell /bin/bash]
                        open an interactive shell in a workspace
  run <id|name> [--no-cleanup] [--timeout 60s] -- <command> [args...]
                        fork workspace, run command, then remove fork

`)
}

// openBrowser attempts to open url in the user's default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	return cmd.Start()
}
