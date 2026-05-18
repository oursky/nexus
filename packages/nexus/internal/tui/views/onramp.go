package views

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
)

// OnrampView combines the no_profile (first-run) and connect_wizard functionality.
// It handles the initial setup flow when no daemon profile exists.

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// LocalCheckMsg is returned after probing whether a local daemon is already
// listening on the configured port.
type LocalCheckMsg struct{ Alive bool }

// DaemonStartDoneMsg is returned when the background daemon-start poll
// completes (successfully or with an error).
type DaemonStartDoneMsg struct{ Err error }

// NoProfileSpinTickMsg drives the spinner animation while checking or starting.
type NoProfileSpinTickMsg struct{}

// WizardSavedMsg is sent after the profile is saved successfully.
type WizardSavedMsg struct{}

// WizardErrMsg is sent when the wizard save/connect attempt fails.
type WizardErrMsg struct{ Err error }

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// CheckLocalDaemonCmd probes the local daemon's /healthz endpoint. It uses a
// short timeout so it never blocks the TUI for long.
func CheckLocalDaemonCmd(port int) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
		c := &http.Client{Timeout: 1 * time.Second}
		resp, err := c.Get(url)
		if err != nil {
			return LocalCheckMsg{Alive: false}
		}
		defer resp.Body.Close()
		return LocalCheckMsg{Alive: resp.StatusCode == http.StatusOK}
	}
}

// StartLocalDaemonCmd launches `nexus daemon start --port <port>` as a
// background subprocess, then polls /healthz until the daemon is ready (up to
// 10 s). It returns DaemonStartDoneMsg with a non-nil error on timeout.
func StartLocalDaemonCmd(port int) tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			exe = "nexus"
		}

		// Start daemon in background; inherit nothing from TUI's terminal.
		cmd := exec.Command(exe, "daemon", "start", "--port", strconv.Itoa(port))
		// Stdout/Stderr left nil → Go runtime connects them to /dev/null.
		if err := cmd.Start(); err != nil {
			return DaemonStartDoneMsg{Err: fmt.Errorf("daemon start: %w", err)}
		}

		// Poll /healthz until ready.
		healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
		client := &http.Client{Timeout: 500 * time.Millisecond}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get(healthURL)
			if err == nil {
				ok := resp.StatusCode == http.StatusOK
				_ = resp.Body.Close()
				if ok {
					return DaemonStartDoneMsg{Err: nil}
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		return DaemonStartDoneMsg{
			Err: fmt.Errorf("daemon did not start within 10 s — run: nexus daemon start --port %d", port),
		}
	}
}

// WizardSaveCmd saves the profile (fetching the remote token if needed) and
// signals the TUI to retry the daemon connection.
func WizardSaveCmd(host, portStr, sshKey string) tea.Cmd {
	return func() tea.Msg {
		h := strings.TrimSpace(host)
		if h == "" {
			h = "localhost"
		}
		portN := 7777
		if p, err := strconv.Atoi(strings.TrimSpace(portStr)); err == nil && p > 0 {
			portN = p
		}
		keyPath := strings.TrimSpace(sshKey)

		p := &profile.Profile{
			Name:            "default",
			Host:            h,
			Port:            portN,
			SSHIdentityFile: keyPath,
		}

		// For remote hosts, SSH-fetch the daemon token.
		if !IsLocalhost(h) {
			tok, err := FetchToken(h, 0, keyPath)
			if err != nil {
				return WizardErrMsg{Err: fmt.Errorf("SSH token fetch failed: %w", err)}
			}
			p.Token = tok
			if err := tokenstore.SaveProfileToken(tok); err != nil {
				return WizardErrMsg{Err: fmt.Errorf("save token: %w", err)}
			}
		}

		if err := profile.SaveDefault(p); err != nil {
			return WizardErrMsg{Err: fmt.Errorf("save profile: %w", err)}
		}
		return WizardSavedMsg{}
	}
}

// NoProfileSpinTick emits a NoProfileSpinTickMsg after 100 ms so the spinner
// animation advances.
func NoProfileSpinTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return NoProfileSpinTickMsg{}
	})
}

// IsLocalhost checks if the given host string refers to localhost.
func IsLocalhost(host string) bool {
	for _, n := range []string{"localhost", "127.0.0.1", "::1", "0.0.0.0"} {
		if strings.EqualFold(host, n) {
			return true
		}
	}
	return false
}

// FetchToken SSHs to the remote host and runs `nexus daemon token` to get auth.
func FetchToken(host string, sshPort int, identityFile string) (string, error) {
	args := []string{host, "$HOME/.local/bin/nexus", "daemon", "token"}
	if sshPort > 0 && sshPort != 22 {
		args = append([]string{"-p", strconv.Itoa(sshPort)}, args...)
	}
	if identityFile != "" {
		args = append([]string{"-i", identityFile, "-o", "IdentitiesOnly=yes"}, args...)
	}
	out, err := exec.Command("ssh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("ssh %s nexus daemon token: %w", host, err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", fmt.Errorf("no token from remote host (is daemon running?)")
	}
	return tok, nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NoProfileConfig holds configuration for rendering the no-profile view.
type NoProfileConfig struct {
	// Width is the available width for the view.
	Width int
	// Checking indicates whether we're checking for a local daemon.
	Checking bool
	// Busy indicates whether we're starting the daemon.
	Busy bool
	// SpinIdx is the current spinner frame index.
	SpinIdx int
	// Err contains any error message to display.
	Err string
	// Sel is the selected menu item (0 = start local, 1 = connect remote).
	Sel int
}

// RenderNoProfile renders the "no daemon / start or connect" view.
func RenderNoProfile(cfg NoProfileConfig) string {
	w := cfg.Width
	if w < 56 {
		w = 56
	}
	innerW := w - 4

	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(strings.Repeat("─", innerW))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("86")).
		Padding(1, 2).
		Width(w)

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Render("nexus"))
	fmt.Fprintf(&b, "%s\n\n", sep)

	spin := spinFrames[cfg.SpinIdx%len(spinFrames)]

	switch {
	case cfg.Checking:
		fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(spin+" Checking for local daemon…"))

	case cfg.Busy:
		fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(spin+" Starting daemon…"))

	default:
		if cfg.Err != "" {
			fmt.Fprintf(&b, "%s\n\n", lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render("✗ "+cfg.Err))
		} else {
			fmt.Fprintf(&b, "%s\n\n", lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render("No daemon running."))
		}

		entries := []struct{ label, hint string }{
			{"Start local daemon", "nexus daemon start"},
			{"Connect to remote host…", "enter host/port/SSH key"},
		}
		for i, e := range entries {
			if i == cfg.Sel {
				fmt.Fprintf(&b, "%s   %s\n",
					lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Render("▶  "+e.label),
					lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("("+e.hint+")"),
				)
			} else {
				fmt.Fprintf(&b, "%s   %s\n",
					"   "+lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(e.label),
					lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("("+e.hint+")"),
				)
			}
		}

		fmt.Fprintf(&b, "\n%s   %s",
			lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Render("[ enter  select ]"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("[ esc  quit ]"),
		)
	}

	return box.Render(b.String())
}
