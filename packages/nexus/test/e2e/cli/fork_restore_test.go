//go:build e2e

package cli_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

func TestCLI_WorkspaceForkAndRestore(t *testing.T) {
	t.Parallel()
	harness.SkipIfVMBoot(t)
	h := harness.NewCLIHarness(t)
	clientRepo := harness.MakeLocalGitRepo(t, "fork-restore-cli")

	runGit := func(args ...string) {
		t.Helper()
		c := exec.Command(args[0], args[1:]...)
		c.Dir = clientRepo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("git", "branch", "feature-for-fork", "main")

	_, daemonRepo := h.MirrorGitToDaemon(t, clientRepo, "proj-fork-restore-cli")

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
			"workspaceName": "fork-cli-base",
		},
	}, &createRes)
	baseID := createRes.Workspace.ID
	if baseID == "" {
		t.Fatal("workspace.create: empty id")
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": baseID}, nil)
	})

	_, err := h.Run(t, clientRepo, "workspace", "start", baseID)
	if err != nil {
		t.Fatalf("workspace start: %v", err)
	}

	out, err := h.Run(t, clientRepo, "workspace", "fork", "fork-cli-base", "--ref", "feature-for-fork", "--name", "fork-cli-child")
	if err != nil {
		t.Fatalf("workspace fork: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "forked workspace") || !strings.Contains(s, "fork-cli-child") {
		t.Fatalf("fork: unexpected output: %s", out)
	}

	childID := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "(id: "); idx >= 0 {
			rest := strings.TrimPrefix(line[idx:], "(id: ")
			rest = strings.TrimSuffix(strings.TrimSpace(rest), ")")
			childID = rest
			break
		}
	}
	if childID == "" {
		out2, err2 := h.Run(t, clientRepo, "workspace", "list")
		if err2 != nil {
			t.Fatalf("workspace list: %v\n%s", err2, out2)
		}
		for _, line := range strings.Split(string(out2), "\n") {
			if strings.Contains(line, "fork-cli-child") {
				fields := strings.Fields(line)
				if len(fields) > 0 && strings.HasPrefix(fields[0], "ws-") {
					childID = fields[0]
					break
				}
			}
		}
	}
	if childID == "" {
		t.Fatalf("could not resolve fork child workspace id from output: %s", s)
	}
	t.Cleanup(func() {
		_ = h.Call("workspace.remove", map[string]any{"id": childID}, nil)
	})

	_, err = h.Run(t, clientRepo, "workspace", "stop", baseID)
	if err != nil {
		t.Fatalf("workspace stop: %v", err)
	}

	out, err = h.Run(t, clientRepo, "workspace", "restore", "fork-cli-base")
	if err != nil {
		t.Fatalf("workspace restore: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "restored workspace") {
		t.Fatalf("restore: unexpected output: %s", out)
	}
}
