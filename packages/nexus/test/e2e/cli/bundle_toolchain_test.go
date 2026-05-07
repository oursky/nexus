//go:build e2e

package cli_test

// Spec: VM-023 (bundle workspace toolchain)

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// TestCLI_BundleWorkspaceToolchain verifies that toolchains (docker, node, make)
// work correctly inside an NX bundle-imported workspace.
func TestCLI_BundleWorkspaceToolchain(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	// TODO(VM-023): workspace import does not yet register the workspace with the
	// daemon, so workspace.list does not return the imported workspace ID. This
	// test requires a daemon-integrated bundle import RPC (workspace.import or
	// workspace.create with BundleFrom) which is not yet implemented.
	// Track: https://github.com/oursky/nexus/issues/<TBD>
	t.Skip("bundle import daemon registration not yet implemented")
	h := cliSuite.NewCLIHarness(t)
	clientRepo := harness.MakeLocalGitRepo(t, "ws-bundle-toolchain")

	_, daemonRepo := h.MirrorGitToDaemon(t, clientRepo, "proj-ws-bundle-toolchain")

	var createRes struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	h.MustCall("workspace.create", map[string]any{
		"spec": map[string]any{
			"repo":          daemonRepo,
			"ref":           "main",
			"workspaceName": "bundle-toolchain-base",
		},
	}, &createRes)
	baseID := createRes.Workspace.ID
	if baseID == "" {
		t.Fatal("workspace.create: empty id")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": baseID}, nil)
	})

	bundlePath := filepath.Join(t.TempDir(), "bundle-toolchain.nxbundle")
	out, err := h.Run(t, clientRepo, "workspace", "export", baseID, "--out", bundlePath)
	if err != nil {
		t.Fatalf("workspace export: %v\n%s", err, out)
	}

	var listBefore struct {
		Workspaces []struct {
			ID string `json:"id"`
		} `json:"workspaces"`
	}
	h.MustCall("workspace.list", map[string]any{}, &listBefore)
	knownIDs := make(map[string]bool)
	for _, ws := range listBefore.Workspaces {
		knownIDs[ws.ID] = true
	}

	out, err = h.Run(t, clientRepo, "workspace", "import", "--from", bundlePath)
	if err != nil {
		t.Fatalf("workspace import: %v\n%s", err, out)
	}

	var listAfter struct {
		Workspaces []struct {
			ID string `json:"id"`
		} `json:"workspaces"`
	}
	h.MustCall("workspace.list", map[string]any{}, &listAfter)
	var importedID string
	for _, ws := range listAfter.Workspaces {
		if !knownIDs[ws.ID] {
			importedID = ws.ID
			break
		}
	}
	if importedID == "" {
		t.Fatal("could not determine imported workspace ID")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": importedID}, nil)
	})

	h.MustCall("workspace.start", map[string]any{"id": importedID}, nil)

	var readyRes struct {
		Ready bool `json:"ready"`
	}
	ready := false
	for attempts := 0; attempts < 120; attempts++ {
		h.MustCall("workspace.ready", map[string]any{"id": importedID}, &readyRes)
		if readyRes.Ready {
			ready = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !ready {
		t.Fatalf("imported workspace %s did not become ready within 120s", importedID)
	}

	// Wait a bit for dockerd to finish starting inside the guest.
	time.Sleep(3 * time.Second)

	cases := []struct {
		name    string
		args    []string
		wantOut string
	}{
		{
			name:    "docker ps header",
			args:    []string{"workspace", "exec", importedID, "--", "sh", "-c", "docker ps; sleep 0.05"},
			wantOut: "CONTAINER ID",
		},
		{
			name:    "node version",
			args:    []string{"workspace", "exec", importedID, "--", "sh", "-c", "node --version; sleep 0.05"},
			wantOut: "v20.",
		},
		{
			name:    "make version",
			args:    []string{"workspace", "exec", importedID, "--", "sh", "-c", "make --version | head -1; sleep 0.05"},
			wantOut: "GNU Make",
		},
		{
			name:    "workspace mount",
			args:    []string{"workspace", "exec", importedID, "--", "sh", "-c", "ls /workspace; sleep 0.05"},
			wantOut: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := h.Run(t, clientRepo, tc.args...)
			if err != nil {
				t.Fatalf("%s: %v\noutput: %s", tc.name, err, out)
			}
			if tc.wantOut != "" && !strings.Contains(string(out), tc.wantOut) {
				t.Errorf("%s: expected %q in output, got %q", tc.name, tc.wantOut, string(out))
			}
		})
	}
}
