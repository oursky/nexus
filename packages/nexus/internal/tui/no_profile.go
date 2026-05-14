package tui

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
)

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// localCheckMsg is returned after probing whether a local daemon is already
// listening on the configured port.
type localCheckMsg struct{ alive bool }

// daemonStartDoneMsg is returned when the background daemon-start poll
// completes (successfully or with an error).
type daemonStartDoneMsg struct{ err error }

// noProfileSpinTickMsg drives the spinner animation while checking or starting.
type noProfileSpinTickMsg struct{}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// checkLocalDaemonCmd probes the local daemon's /healthz endpoint.  It uses a
// short timeout so it never blocks the TUI for long.
func checkLocalDaemonCmd(port int) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
		c := &http.Client{Timeout: 1 * time.Second}
		resp, err := c.Get(url)
		if err != nil {
			return localCheckMsg{alive: false}
		}
		defer resp.Body.Close()
		return localCheckMsg{alive: resp.StatusCode == http.StatusOK}
	}
}

// startLocalDaemonCmd launches `nexus daemon start --port <port>` as a
// background subprocess, then polls /healthz until the daemon is ready (up to
// 10 s).  It returns daemonStartDoneMsg with a non-nil error on timeout.
func startLocalDaemonCmd(port int) tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			exe = "nexus"
		}

		// Start daemon in background; inherit nothing from TUI's terminal.
		cmd := exec.Command(exe, "daemon", "start", "--port", strconv.Itoa(port))
		// Stdout/Stderr left nil → Go runtime connects them to /dev/null.
		if err := cmd.Start(); err != nil {
			return daemonStartDoneMsg{err: fmt.Errorf("daemon start: %w", err)}
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
					return daemonStartDoneMsg{err: nil}
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		return daemonStartDoneMsg{
			err: fmt.Errorf("daemon did not start within 10 s — run: nexus daemon start --port %d", port),
		}
	}
}

// saveLocalProfileCmd saves a localhost profile with the given port using the
// shared wizard save path (which handles token loading from the keystore).
func saveLocalProfileCmd(port int) tea.Cmd {
	return wizardSaveCmd("localhost", strconv.Itoa(port), "")
}

// noProfileSpinTick emits a noProfileSpinTickMsg after 100 ms so the spinner
// animation advances.
func noProfileSpinTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return noProfileSpinTickMsg{}
	})
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

// handleNoProfileKeys handles keyboard input while the no-profile view is
// visible.
func (m Model) handleNoProfileKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Always allow quit, even while busy.
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.quitting = true
		return m, tea.Quit
	}

	// Ignore all other keys while a background operation is in progress.
	if m.noProfileBusy || m.noProfileChecking {
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		if m.noProfileSel > 0 {
			m.noProfileSel--
		}

	case "down", "j":
		if m.noProfileSel < 1 {
			m.noProfileSel++
		}

	case "enter", " ":
		switch m.noProfileSel {
		case 0:
			// Start local daemon.
			m.noProfileBusy = true
			m.noProfileErr = ""
			return m, tea.Batch(startLocalDaemonCmd(m.localPort), noProfileSpinTick())
		case 1:
			// Connect to remote host — open the existing wizard form.
			m.showNoProfile = false
			m.showWizard = true
			m.wizardStep = 0
			m.wizardHostTI, m.wizardPortTI, m.wizardKeyTI = newWizardInputs()
			return m, m.wizardHostTI.Focus()
		}
	}

	return m, nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

var noProfileSpinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// renderNoProfile renders the "no daemon / start or connect" view.
func renderNoProfile(m *Model) string {
	w := m.listWidth
	if w < 56 {
		w = 56
	}
	innerW := w - 4

	sep := separatorStyle.Render(strings.Repeat("─", innerW))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(w)

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", titleStyle.Render("nexus"))
	fmt.Fprintf(&b, "%s\n\n", sep)

	spin := noProfileSpinFrames[m.noProfileSpinIdx%len(noProfileSpinFrames)]

	switch {
	case m.noProfileChecking:
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(spin+" Checking for local daemon…"))

	case m.noProfileBusy:
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(spin+" Starting daemon…"))

	default:
		if m.noProfileErr != "" {
			fmt.Fprintf(&b, "%s\n\n", statusErrStyle.Render("✗ "+m.noProfileErr))
		} else {
			fmt.Fprintf(&b, "%s\n\n", warningStyle.Render("No daemon running."))
		}

		entries := []struct{ label, hint string }{
			{"Start local daemon", "nexus daemon start"},
			{"Connect to remote host…", "enter host/port/SSH key"},
		}
		for i, e := range entries {
			if i == m.noProfileSel {
				fmt.Fprintf(&b, "%s   %s\n",
					accentStyle.Render("▶  "+e.label),
					mutedStyle.Render("("+e.hint+")"),
				)
			} else {
				fmt.Fprintf(&b, "%s   %s\n",
					"   "+detailKeyStyle.Render(e.label),
					mutedStyle.Render("("+e.hint+")"),
				)
			}
		}

		fmt.Fprintf(&b, "\n%s   %s",
			accentStyle.Render("[ enter  select ]"),
			mutedStyle.Render("[ esc  quit ]"),
		)
	}

	return box.Render(b.String())
}
