package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inizio/nexus/packages/nexus/pkg/workspacemgr"
)

// ── JSON-RPC types ─────────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// daemonRPC sends a single JSON-RPC request and decodes the result into out.
func daemonRPC(conn *websocket.Conn, method string, params interface{}, out interface{}) error {
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Method:  method,
		Params:  params,
	}
	if err := conn.WriteJSON(req); err != nil {
		return fmt.Errorf("rpc send: %w", err)
	}
	var resp rpcResponse
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

// rpcDo is a context-aware wrapper around daemonRPC.
func rpcDo(_ context.Context, conn *websocket.Conn, method string, params, result any) error {
	return daemonRPC(conn, method, params, result)
}

// ── Output helpers ─────────────────────────────────────────────────────────────

// printJSON marshals v as indented JSON to stdout. Exits with code 1 on error.
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "json encode: %v\n", err)
		os.Exit(1)
	}
}

// confirmPrompt prints "msg [y/N]: " and returns true if the user answers "y" or "yes".
func confirmPrompt(msg string) bool {
	fmt.Printf("%s [y/N]: ", msg)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return ans == "y" || ans == "yes"
}

// ── Resolve helpers ────────────────────────────────────────────────────────────

// resolveWorkspaceID resolves a workspace name-or-ID to a canonical ID.
// It calls workspace.list and matches by exact ID first, then by name.
// Returns an error if ambiguous or not found.
func resolveWorkspaceID(ctx context.Context, conn *websocket.Conn, nameOrID string) (string, error) {
	var result struct {
		Workspaces []workspacemgr.Workspace `json:"workspaces"`
	}
	if err := rpcDo(ctx, conn, "workspace.list", map[string]any{}, &result); err != nil {
		return "", fmt.Errorf("workspace.list: %w", err)
	}

	for _, ws := range result.Workspaces {
		if ws.ID == nameOrID {
			return ws.ID, nil
		}
	}

	var matches []workspacemgr.Workspace
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

// resolveProjectID resolves a project name-or-ID to a canonical ID.
// It calls project.list and matches by exact ID first, then by name.
// Returns an error if ambiguous or not found.
func resolveProjectID(ctx context.Context, conn *websocket.Conn, nameOrID string) (string, error) {
	var result struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := rpcDo(ctx, conn, "project.list", map[string]any{}, &result); err != nil {
		return "", fmt.Errorf("project.list: %w", err)
	}

	for _, p := range result.Projects {
		if p.ID == nameOrID {
			return p.ID, nil
		}
	}

	type proj = struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var matches []proj
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
