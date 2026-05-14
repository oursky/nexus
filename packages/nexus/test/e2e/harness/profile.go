//go:build e2e

package harness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/oursky/nexus/packages/nexus/internal/clientstate"
)

// writeE2EProfile writes the default profile to a SQLite client state DB under
// configHome so the CLI subprocess uses EnsureDaemon → SSH tunnel → remote
// daemon port (same code path as a real remote box).
//
// Both XDG_CONFIG_HOME and XDG_STATE_HOME are set to configHome in cliEnv so
// that the token file and the profile DB share the same temp directory.
func writeE2EProfile(t *testing.T, configHome, sshHost string, daemonPort int, token string, sshPort int) {
	t.Helper()

	if sshHost == "" {
		u := os.Getenv("USER")
		if u == "" {
			u = "runner"
		}
		sshHost = u + "@127.0.0.1"
	}

	// Write the profile to SQLite at $configHome/nexus/client.db
	// (XDG_STATE_HOME=configHome in subprocess env).
	dbPath := filepath.Join(configHome, "nexus", "client.db")
	db, err := clientstate.OpenAt(dbPath)
	if err != nil {
		t.Fatalf("writeE2EProfile: open client state: %v", err)
	}
	defer db.Close()

	if err := db.UpsertProfile(clientstate.Profile{
		Name:      "default",
		Host:      sshHost,
		Port:      daemonPort,
		SSHPort:   sshPort,
		IsDefault: true,
	}); err != nil {
		t.Fatalf("writeE2EProfile: upsert profile: %v", err)
	}

	// Token lives in a plain file at $configHome/nexus/daemon-token
	// (XDG_CONFIG_HOME=configHome → tokenstore.DefaultLinuxFilePath()).
	if token != "" {
		tokenPath := filepath.Join(configHome, "nexus", "daemon-token")
		if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
			t.Fatalf("writeE2EProfile: mkdir token dir: %v", err)
		}
		if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
			t.Fatalf("writeE2EProfile: write token: %v", err)
		}
	}
	t.Logf("e2e: wrote profile at %s (host=%s port=%d sshPort=%d)", dbPath, sshHost, daemonPort, sshPort)
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

// E2EUseRemoteProfile reports whether CLI subprocesses should use the default
// profile + SSH tunnel (production-like) instead of NEXUS_E2E_DAEMON_WEBSOCKET.
func E2EUseRemoteProfile() bool {
	v := strings.TrimSpace(os.Getenv("NEXUS_E2E_REMOTE_PROFILE"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// PrepareE2EConfigHome returns a temp dir populated with an e2e profile and
// token, ready to be used as both XDG_CONFIG_HOME and XDG_STATE_HOME.
func PrepareE2EConfigHome(t *testing.T, daemonPort int, token string) string {
	t.Helper()
	configHome := t.TempDir()
	writeE2EProfile(t, configHome, sshHostFromEnv(), daemonPort, token, sshPortFromEnv())
	return configHome
}

// cliEnv returns env vars for a nexus subprocess: either direct WebSocket
// (default) or profile + XDG dirs when NEXUS_E2E_REMOTE_PROFILE is enabled.
func (c *CLIHarness) cliEnv(configHome string) []string {
	base := os.Environ()
	if E2EUseRemoteProfile() {
		out := make([]string, 0, len(base)+5)
		for _, e := range base {
			if strings.HasPrefix(e, "NEXUS_E2E_DAEMON_WEBSOCKET=") ||
				strings.HasPrefix(e, "NEXUS_DAEMON_TOKEN=") {
				continue
			}
			out = append(out, e)
		}
		// Both config (token file) and state (profile DB) share the same dir.
		out = append(out, "XDG_CONFIG_HOME="+configHome)
		out = append(out, "XDG_STATE_HOME="+configHome)
		if id := strings.TrimSpace(os.Getenv("NEXUS_E2E_SSH_IDENTITY")); id != "" {
			out = append(out, "NEXUS_E2E_SSH_IDENTITY="+id)
		}
		return out
	}
	extra := []string{
		"NEXUS_E2E_DAEMON_WEBSOCKET=" + c.wsURL,
		"NEXUS_DAEMON_TOKEN=" + c.token,
	}
	return append(base, extra...)
}

// ConfigHomeForCLI returns the per-harness XDG dir when using remote profile mode.
func (c *CLIHarness) ConfigHomeForCLI(t *testing.T) string {
	t.Helper()
	if c.configHome == "" {
		c.configHome = PrepareE2EConfigHome(t, c.daemonPort, c.token)
	}
	return c.configHome
}
