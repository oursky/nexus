package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/sshtunnel"
)

type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// tunnelCache caches the active tunnel so it's reused across commands.
var tunnelCache struct {
	sync.Once
	tunnel *sshtunnel.Manager
	port   int
	err    error
}

// envE2EDaemonWebSocket, when set, makes EnsureDaemon dial this WebSocket URL with
// NEXUS_DAEMON_TOKEN (bearer auth) instead of using the default profile + SSH tunnel.
// Used by integration / e2e tests that run the CLI against a local networked daemon.
const envE2EDaemonWebSocket = "NEXUS_E2E_DAEMON_WEBSOCKET"

func EnsureDaemon() (*websocket.Conn, error) {
	return EnsureDaemonVerbose(false)
}

func EnsureDaemonVerbose(verbose bool) (*websocket.Conn, error) {
	if ws := strings.TrimSpace(os.Getenv(envE2EDaemonWebSocket)); ws != "" {
		return dialDaemonWebSocket(ws)
	}

	p, err := profile.LoadDefault()
	if err != nil {
		return nil, err
	}

	tunnelCache.Do(func() {
		var tm *sshtunnel.Manager
		if verbose {
			tm = sshtunnel.NewVerbose(p.Host, p.Port, p.SSHPort)
		} else {
			tm = sshtunnel.New(p.Host, p.Port, p.SSHPort)
		}
		localPort, err := tm.Ensure()
		if err != nil {
			tm.Close()
			tunnelCache.err = fmt.Errorf("ssh tunnel to %s: %w", p.Host, err)
			return
		}
		tunnelCache.tunnel = tm
		tunnelCache.port = localPort
	})
	if tunnelCache.err != nil {
		return nil, tunnelCache.err
	}

	url := fmt.Sprintf("ws://localhost:%d/", tunnelCache.port)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+p.Token)
	if verbose {
		fmt.Fprintf(os.Stderr, "[nexus] connecting to daemon: %s\n", url)
	}
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	return conn, nil
}

func dialDaemonWebSocket(wsURL string) (*websocket.Conn, error) {
	tok := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_TOKEN"))
	if tok == "" {
		return nil, fmt.Errorf("%s is set but NEXUS_DAEMON_TOKEN is empty (bearer token required)", envE2EDaemonWebSocket)
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+tok)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon websocket: %w", err)
	}
	return conn, nil
}

func Do(conn *websocket.Conn, method string, params interface{}, out interface{}) error {
	req := Request{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Method:  method,
		Params:  params,
	}
	if err := conn.WriteJSON(req); err != nil {
		return fmt.Errorf("rpc send: %w", err)
	}
	var resp Response
	if err := conn.ReadJSON(&resp); err != nil {
		return fmt.Errorf("rpc recv: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if out != nil && resp.Result != nil {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

func DoCtx(_ context.Context, conn *websocket.Conn, method string, params, result any) error {
	return Do(conn, method, params, result)
}

func PrintJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "json encode: %v\n", err)
		os.Exit(1)
	}
}

func UnmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func ConfirmPrompt(msg string) bool {
	fmt.Printf("%s [y/N]: ", msg)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return ans == "y" || ans == "yes"
}

// ResolveWorkspaceIDMux resolves a workspace name or ID using a MuxConn.
func ResolveWorkspaceIDMux(ctx context.Context, conn *MuxConn, nameOrID string) (string, error) {
	var result struct {
		Workspaces []workspace.Workspace `json:"workspaces"`
	}
	if err := conn.Call("workspace.list", map[string]any{}, &result); err != nil {
		return "", fmt.Errorf("workspace.list: %w", err)
	}

	for _, ws := range result.Workspaces {
		if ws.ID == nameOrID {
			return ws.ID, nil
		}
	}

	var matches []workspace.Workspace
	for _, ws := range result.Workspaces {
		if ws.WorkspaceName == nameOrID {
			matches = append(matches, ws)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no workspace found with id or name %q", nameOrID)
	case 1:
		return matches[0].ID, nil
	default:
		ids := make([]string, len(matches))
		for i, ws := range matches {
			ids[i] = ws.ID
		}
		return "", fmt.Errorf("ambiguous workspace name %q matches multiple IDs: %s", nameOrID, strings.Join(ids, ", "))
	}
}

func ResolveWorkspaceID(ctx context.Context, conn *websocket.Conn, nameOrID string) (string, error) {
	var result struct {
		Workspaces []workspace.Workspace `json:"workspaces"`
	}
	if err := DoCtx(ctx, conn, "workspace.list", map[string]any{}, &result); err != nil {
		return "", fmt.Errorf("workspace.list: %w", err)
	}

	for _, ws := range result.Workspaces {
		if ws.ID == nameOrID {
			return ws.ID, nil
		}
	}

	var matches []workspace.Workspace
	for _, ws := range result.Workspaces {
		if ws.WorkspaceName == nameOrID {
			matches = append(matches, ws)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no workspace found with id or name %q", nameOrID)
	case 1:
		return matches[0].ID, nil
	default:
		ids := make([]string, len(matches))
		for i, ws := range matches {
			ids[i] = ws.ID
		}
		return "", fmt.Errorf("ambiguous workspace name %q matches multiple IDs: %s", nameOrID, strings.Join(ids, ", "))
	}
}

func ResolveProjectID(ctx context.Context, conn *websocket.Conn, nameOrID string) (string, error) {
	var result struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := DoCtx(ctx, conn, "project.list", map[string]any{}, &result); err != nil {
		return "", fmt.Errorf("project.list: %w", err)
	}

	for _, p := range result.Projects {
		if p.ID == nameOrID {
			return p.ID, nil
		}
	}

	var matches []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	for _, p := range result.Projects {
		if p.Name == nameOrID {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no project found with id or name %q", nameOrID)
	case 1:
		return matches[0].ID, nil
	default:
		ids := make([]string, len(matches))
		for i, p := range matches {
			ids[i] = p.ID
		}
		return "", fmt.Errorf("ambiguous project name %q matches multiple IDs: %s", nameOrID, strings.Join(ids, ", "))
	}
}
