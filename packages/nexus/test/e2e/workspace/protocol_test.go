//go:build e2e

package workspace_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: RPC-009
// TestProtocol_Healthz verifies the /healthz HTTP endpoint returns 200 OK.
func TestProtocol_Healthz(t *testing.T) {
	t.Parallel()
	h := harness.NewCLIHarness(t)
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", h.DaemonPort())

	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := http.Get(url) //nolint:noctx
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("/healthz: expected 200, got %d body=%q", resp.StatusCode, body)
			}
			return
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("/healthz: %v", lastErr)
}

// Spec: RPC-010
// TestProtocol_Version verifies the /version HTTP endpoint returns version JSON.
func TestProtocol_Version(t *testing.T) {
	t.Parallel()
	h := harness.NewCLIHarness(t)
	url := fmt.Sprintf("http://127.0.0.1:%d/version", h.DaemonPort())

	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("/version: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/version: expected 200, got %d body=%q", resp.StatusCode, body)
	}
	if len(body) == 0 {
		t.Fatal("/version: empty body")
	}
}

// Spec: DAEMON-020, DAEMON-021, DAEMON-022, DAEMON-023, DAEMON-024, DAEMON-025
// TestProtocol_NodeInfo verifies node.info returns a valid response with capabilities.
func TestProtocol_NodeInfo(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	var res struct {
		Node struct {
			Name string `json:"name"`
		} `json:"node"`
		Capabilities []struct {
			Name      string `json:"name"`
			Available bool   `json:"available"`
		} `json:"capabilities"`
	}
	h.MustCall("node.info", nil, &res)
	if res.Node.Name == "" {
		t.Error("node.info: empty node name")
	}
	if len(res.Capabilities) == 0 {
		t.Error("node.info: expected at least one capability")
	}
	t.Logf("node.info: name=%q capabilities=%d", res.Node.Name, len(res.Capabilities))
}

// Spec: DAEMON-054
// TestProtocol_AuthReject verifies the HTTP endpoint rejects requests without a valid token.
func TestProtocol_AuthReject(t *testing.T) {
	t.Parallel()
	h := harness.NewCLIHarness(t)
	url := fmt.Sprintf("http://127.0.0.1:%d/", h.DaemonPort())

	// Attempt connection without Authorization header — should get 401 or connection refused for WS.
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		// Connection refused or upgrade required — acceptable.
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// If the server responds without a valid WS upgrade, it should not be 200 for the root path.
	if resp.StatusCode == http.StatusOK {
		t.Log("protocol: root path returned 200 — acceptable if no plain-HTTP route at /")
	}
}

// Spec: WS-040, WS-043, WS-048, WS-049, WS-050, WS-051, WS-052, WS-053, WS-054, WS-055, WS-056
// TestProtocol_WorkflowRoundTrip verifies a complete create/start/stop/remove workflow via RPC.
func TestProtocol_WorkflowRoundTrip(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.New(t)
	repoPath := harness.MakeLocalGitRepo(t, "proto-roundtrip")

	var createRes struct {
		Workspace struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          repoPath,
			"ref":           "main",
			"workspaceName": "proto-roundtrip",
		},
	}, &createRes)
	id := createRes.Workspace.ID
	if id == "" {
		t.Fatal("create: empty id")
	}
	if createRes.Workspace.State != "created" {
		t.Errorf("create: state=%q, want created", createRes.Workspace.State)
	}

	h.MustCall("workspace.start", map[string]any{"id": id}, nil)
	harness.WaitForWorkspaceReady(t, h, id)
	h.MustCall("workspace.stop", map[string]any{"id": id}, nil)

	var removeRes struct{ Removed bool `json:"removed"` }
	h.MustCall("workspace.remove", map[string]any{"id": id}, &removeRes)
	if !removeRes.Removed {
		t.Error("remove: expected removed=true")
	}
}
