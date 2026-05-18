package commands

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// ConnectDaemon initiates connection to the daemon.
func ConnectDaemon(host string, port int) tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement actual connection via rpc.MuxConn
		// For now, return simulated success
		return messages.DaemonConnected{}
	}
}

// DisconnectDaemon closes the daemon connection.
func DisconnectDaemon() tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement actual disconnect
		return messages.DaemonDisconnected{Error: nil}
	}
}

// CheckDaemonStatus checks if daemon is responsive.
func CheckDaemonStatus() tea.Cmd {
	return func() tea.Msg {
		// TODO: Implement actual health check
		return messages.ConnectionStatusMsg{Status: "connected"}
	}
}

// CheckLocalDaemonCmd probes the local daemon's /healthz endpoint.
func CheckLocalDaemonCmd(port int) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
		c := &http.Client{Timeout: 1 * time.Second}
		resp, err := c.Get(url)
		if err != nil {
			return messages.LocalCheckMsg{Alive: false}
		}
		defer resp.Body.Close()
		return messages.LocalCheckMsg{Alive: resp.StatusCode == http.StatusOK}
	}
}

// StartLocalDaemonCmd launches nexus daemon start as a background subprocess,
// then polls /healthz until ready. Returns DaemonStartDoneMsg.
func StartLocalDaemonCmd(port int) tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			exe = "nexus"
		}
		cmd := exec.Command(exe, "daemon", "start", "--port", strconv.Itoa(port))
		if err := cmd.Start(); err != nil {
			return messages.DaemonStartDoneMsg{Err: fmt.Errorf("daemon start: %w", err)}
		}
		healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
		client := &http.Client{Timeout: 500 * time.Millisecond}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get(healthURL)
			if err == nil {
				ok := resp.StatusCode == http.StatusOK
				_ = resp.Body.Close()
				if ok {
					return messages.DaemonStartDoneMsg{Err: nil}
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		return messages.DaemonStartDoneMsg{
			Err: fmt.Errorf("daemon did not start within 10 s"),
		}
	}
}

// SaveLocalProfileCmd saves a localhost profile with the given port
// by loading the local token and saving a profile via the profile package.
func SaveLocalProfileCmd(port int) tea.Cmd {
	return WizardSaveCmd("localhost", strconv.Itoa(port), "")
}

// WizardSaveCmd loads the local token, saves a profile, and returns a ConnReadyMsg on success.
func WizardSaveCmd(host string, portStr string, sshKey string) tea.Cmd {
	return func() tea.Msg {
		port, _ := strconv.Atoi(portStr)
		if port == 0 {
			port = 7777
		}
		// Load or generate local daemon token
		token, err := tokenstore.LoadOrGenerate()
		if err != nil {
			return messages.ConnFailedMsg{Error: fmt.Errorf("token: %w", err)}
		}
		_ = sshKey // future: use SSH to fetch remote token

		// Save profile
		p := &profile.Profile{
			Host:  host,
			Port:  port,
			Token: token,
		}
		if err := profile.SaveDefault(p); err != nil {
			return messages.ConnFailedMsg{Error: fmt.Errorf("save profile: %w", err)}
		}

		// Create connection
		mux, err := rpc.EnsureMux()
		if err != nil {
			return messages.ConnFailedMsg{Error: err}
		}
		return messages.ConnReadyMsg{Mux: mux}
	}
}

// NoProfileSpinTick emits a NoProfileSpinTickMsg after 100ms.
func NoProfileSpinTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return messages.NoProfileSpinTickMsg{}
	})
}
