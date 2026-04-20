//go:build e2e

package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/inizio/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/profile"
)

// writeE2EProfile writes default.json under a fresh XDG_CONFIG_HOME so the CLI uses
// EnsureDaemon → SSH tunnel → remote daemon port (same code path as a real remote box).
func writeE2EProfile(t *testing.T, configHome, sshHost string, daemonPort int, token string, sshPort int) {
	t.Helper()
	dir := filepath.Join(configHome, "nexus", "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("writeE2EProfile: mkdir: %v", err)
	}
	if sshHost == "" {
		u := os.Getenv("USER")
		if u == "" {
			u = "runner"
		}
		sshHost = u + "@127.0.0.1"
	}
	p := &profile.Profile{
		Name:    "default",
		Host:    sshHost,
		Port:    daemonPort,
		Token:   token,
		SSHPort: sshPort,
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("writeE2EProfile: marshal: %v", err)
	}
	path := filepath.Join(dir, "default.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writeE2EProfile: write: %v", err)
	}
	// Token is json:"-" so it is not in the JSON file; persist to the headless
	// file store so LoadDefault() can retrieve it.
	//
	// Use a sub-directory of configHome as XDG_STATE_HOME so both the test
	// process and CLI subprocesses use a writable, per-test-isolated path
	// (avoids permission-denied when /home/runner/.config/nexus/ is root-owned
	// from a previous sudo daemon run).
	if token != "" {
		stateHome := filepath.Join(configHome, ".state")
		if err := os.MkdirAll(stateHome, 0o755); err != nil {
			t.Fatalf("writeE2EProfile: mkdir state: %v", err)
		}
		t.Setenv("XDG_STATE_HOME", stateHome)
		if err := tokenstore.SaveProfileToken(token); err != nil {
			t.Fatalf("writeE2EProfile: save token: %v", err)
		}
	}
	t.Logf("e2e: wrote profile at %s (host=%s port=%d sshPort=%d)", path, sshHost, daemonPort, sshPort)
}

func sshPortFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("NEXUS_E2E_SSH_PORT"))
	if raw == "" {
		return 22
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 22
	}
	return n
}

func sshHostFromEnv() string {
	h := strings.TrimSpace(os.Getenv("NEXUS_E2E_SSH_HOST"))
	if h != "" {
		return h
	}
	u := strings.TrimSpace(os.Getenv("USER"))
	if u == "" {
		u = "runner"
	}
	return u + "@127.0.0.1"
}

// E2EUseRemoteProfile reports whether CLI subprocesses should use the default profile +
// SSH tunnel (production-like) instead of NEXUS_E2E_DAEMON_WEBSOCKET.
func E2EUseRemoteProfile() bool {
	v := strings.TrimSpace(os.Getenv("NEXUS_E2E_REMOTE_PROFILE"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// PrepareE2EConfigHome returns XDG_CONFIG_HOME for CLI runs (remote profile mode).
func PrepareE2EConfigHome(t *testing.T, daemonPort int, token string) string {
	t.Helper()
	configHome := t.TempDir()
	writeE2EProfile(t, configHome, sshHostFromEnv(), daemonPort, token, sshPortFromEnv())
	return configHome
}

// cliEnv returns env vars for a nexus subprocess: either direct WebSocket (default)
// or profile + XDG_CONFIG_HOME when NEXUS_E2E_REMOTE_PROFILE is enabled.
func (c *CLIHarness) cliEnv(configHome string) []string {
	base := os.Environ()
	if E2EUseRemoteProfile() {
		out := make([]string, 0, len(base)+4)
		for _, e := range base {
			if strings.HasPrefix(e, "NEXUS_E2E_DAEMON_WEBSOCKET=") ||
				strings.HasPrefix(e, "NEXUS_DAEMON_TOKEN=") {
				continue
			}
			out = append(out, e)
		}
		out = append(out, "XDG_CONFIG_HOME="+configHome)
		if id := strings.TrimSpace(os.Getenv("NEXUS_E2E_SSH_IDENTITY")); id != "" {
			out = append(out, "NEXUS_E2E_SSH_IDENTITY="+id) // tunnel reads this; documented in sshtunnel
		}
		// Forward the per-test XDG_STATE_HOME so the CLI subprocess resolves
		// the profile token from the same writable location as the test process.
		if sh := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); sh != "" {
			out = append(out, "XDG_STATE_HOME="+sh)
		}
		return out
	}
	extra := []string{
		"NEXUS_E2E_DAEMON_WEBSOCKET=" + c.wsURL,
		"NEXUS_DAEMON_TOKEN=" + c.token,
	}
	return append(base, extra...)
}

// ConfigHomeForCLI returns the per-harness XDG_CONFIG_HOME when using remote profile mode.
func (c *CLIHarness) ConfigHomeForCLI(t *testing.T) string {
	t.Helper()
	if c.configHome == "" {
		c.configHome = PrepareE2EConfigHome(t, c.daemonPort, c.token)
	}
	return c.configHome
}
