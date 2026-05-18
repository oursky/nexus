package tui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
)

// wizardSavedMsg is sent after the profile is saved successfully.
type wizardSavedMsg struct{}

// wizardErrMsg is sent when the wizard save/connect attempt fails.
type wizardErrMsg struct{ err error }

// newWizardInputs creates fresh text inputs for the connect wizard.
func newWizardInputs() (host, port, sshKey textinput.Model) {
	host = textinput.New()
	host.Placeholder = "hostname or IP  (empty = localhost)"
	host.CharLimit = 256
	host.Width = 52

	port = textinput.New()
	port.Placeholder = "7777"
	port.SetValue("7777")
	port.CharLimit = 8
	port.Width = 8

	sshKey = textinput.New()
	sshKey.Placeholder = "~/.ssh/id_ed25519"
	sshKey.CharLimit = 256
	sshKey.Width = 36

	return host, port, sshKey
}

// refocusWizard blurs all wizard inputs and focuses the active field.
func refocusWizard(m Model) (Model, tea.Cmd) {
	m.wizardHostTI.Blur()
	m.wizardPortTI.Blur()
	m.wizardKeyTI.Blur()
	var cmd tea.Cmd
	switch m.wizardStep {
	case 0:
		cmd = m.wizardHostTI.Focus()
	case 1:
		cmd = m.wizardPortTI.Focus()
	case 2:
		cmd = m.wizardKeyTI.Focus()
	}
	return m, cmd
}

// wizardSaveCmd saves the profile (fetching the remote token if needed) and
// signals the TUI to retry the daemon connection.
func wizardSaveCmd(host, portStr, sshKey string) tea.Cmd {
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
		if !wizardIsLocalhost(h) {
			tok, err := wizardFetchToken(h, 0, keyPath)
			if err != nil {
				return wizardErrMsg{err: fmt.Errorf("SSH token fetch failed: %w", err)}
			}
			p.Token = tok
			if err := tokenstore.SaveProfileToken(tok); err != nil {
				return wizardErrMsg{err: fmt.Errorf("save token: %w", err)}
			}
		}

		if err := profile.SaveDefault(p); err != nil {
			return wizardErrMsg{err: fmt.Errorf("save profile: %w", err)}
		}
		return wizardSavedMsg{}
	}
}

func wizardIsLocalhost(host string) bool {
	for _, n := range []string{"localhost", "127.0.0.1", "::1", "0.0.0.0"} {
		if strings.EqualFold(host, n) {
			return true
		}
	}
	return false
}

func wizardFetchToken(host string, sshPort int, identityFile string) (string, error) {
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

// handleWizardKeys handles keyboard input while the connect wizard is visible.
func (m Model) handleWizardKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.wizardBusy {
		// Only allow quit while connecting.
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit

	case "esc":
		// Skip wizard: attempt localhost connection immediately.
		m.wizardBusy = true
		m.wizardErr = ""
		return m, wizardSaveCmd("localhost", "7777", "")

	case "tab":
		m.wizardStep = (m.wizardStep + 1) % 3
		return refocusWizard(m)

	case "shift+tab":
		m.wizardStep = (m.wizardStep + 2) % 3
		return refocusWizard(m)

	case "enter":
		switch m.wizardStep {
		case 0:
			m.wizardStep = 1
			return refocusWizard(m)
		case 1:
			m.wizardStep = 2
			return refocusWizard(m)
		default:
			// Submit.
			m.wizardBusy = true
			m.wizardErr = ""
			h := m.wizardHostTI.Value()
			p := m.wizardPortTI.Value()
			k := m.wizardKeyTI.Value()
			return m, wizardSaveCmd(h, p, k)
		}

	default:
		var cmd tea.Cmd
		switch m.wizardStep {
		case 0:
			m.wizardHostTI, cmd = m.wizardHostTI.Update(msg)
		case 1:
			m.wizardPortTI, cmd = m.wizardPortTI.Update(msg)
		case 2:
			m.wizardKeyTI, cmd = m.wizardKeyTI.Update(msg)
		}
		return m, cmd
	}
}

// renderWizard renders the connect wizard view.
func renderWizard(m *Model) string {
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
	fmt.Fprintf(&b, "%s\n", titleStyle.Render("Connect to daemon"))
	fmt.Fprintf(&b, "%s\n\n", sep)

	var hostLabel, portLabel, keyLabel string
	switch m.wizardStep {
	case 0:
		hostLabel = accentStyle.Render("Host")
		portLabel = detailKeyStyle.Render("Port")
		keyLabel = detailKeyStyle.Render("SSH key")
	case 1:
		hostLabel = detailKeyStyle.Render("Host")
		portLabel = accentStyle.Render("Port")
		keyLabel = detailKeyStyle.Render("SSH key")
	default:
		hostLabel = detailKeyStyle.Render("Host")
		portLabel = detailKeyStyle.Render("Port")
		keyLabel = accentStyle.Render("SSH key")
	}

	fmt.Fprintf(&b, "%s\n%s\n\n", hostLabel, m.wizardHostTI.View())
	fmt.Fprintf(&b, "%s  %s    %s  %s\n\n",
		portLabel, m.wizardPortTI.View(),
		keyLabel, m.wizardKeyTI.View(),
	)

	if m.wizardBusy {
		fmt.Fprintf(&b, "%s\n\n", mutedStyle.Render("● connecting…"))
	} else if m.wizardErr != "" {
		fmt.Fprintf(&b, "%s\n\n", statusErrStyle.Render("✗ "+m.wizardErr))
	} else {
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "%s   %s",
		accentStyle.Render("[ enter  connect ]"),
		mutedStyle.Render("[ esc  skip / local ]"),
	)

	return box.Render(b.String())
}
