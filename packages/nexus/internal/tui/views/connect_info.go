package views

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
)

// ConnectInstructions holds UI copy for SSH and remote editors.
type ConnectInstructions struct {
	SSHCommand       string
	ProxyJump        string
	GuestTarget      string
	HostAlias        string
	VSCodiumHint     string
	CursorHint       string
	ProcessShellHint string
	Notes            []string
}

// BuildConnectInstructions builds SSH and editor connection instructions for a workspace.
func BuildConnectInstructions(ws *domainws.Workspace) ConnectInstructions {
	if ws == nil {
		return ConnectInstructions{Notes: []string{"No workspace selected."}}
	}
	hostAlias := "nexus-vm-" + ws.ID
	out := ConnectInstructions{
		HostAlias: hostAlias,
		Notes:     nil,
	}

	if !domainws.UsesGuestVM(ws.Backend) {
		out.ProcessShellHint = fmt.Sprintf("This workspace uses the %q backend (no guest VM SSH). Use:", ws.Backend)
		out.SSHCommand = fmt.Sprintf("  (interactive) use Terminal action, or run: nexus workspace shell %s", ws.ID)
		out.VSCodiumHint = "VS Code Remote SSH applies to VM backends. For process sandbox, open files from the synced repo on the host."
		out.CursorHint = "Cursor Remote SSH applies to VM backends. For process sandbox, open the repo locally."
		return out
	}

	if ws.GuestIP == "" {
		out.Notes = append(out.Notes, "Guest VM is not up (no guest IP). Start the workspace first (s).")
		out.VSCodiumHint = fmt.Sprintf("After start: run `nexus workspace open-editor %s --app vscode` to write SSH config and open.", ws.ID)
		out.CursorHint = fmt.Sprintf("After start: run `nexus workspace open-editor %s --app cursor` to write SSH config and open.", ws.ID)
		return out
	}

	p, _ := profile.LoadDefault()
	out.ProxyJump = ProxyJumpFromProfile(p)

	sshHost, sshPort, err := ParseSSHHostPort(ws.GuestIP)
	if err != nil {
		out.Notes = append(out.Notes, "Invalid guest IP: "+err.Error())
		return out
	}
	sshTarget := "root@" + sshHost
	out.GuestTarget = sshTarget
	args := BuildVMSSHArgs(out.ProxyJump, sshPort)
	full := append(append([]string{"ssh"}, args...), sshTarget)
	out.SSHCommand = strings.Join(full, " ")

	out.Notes = append(out.Notes, "Copy the SSH command above. Ensure `nexus workspace open-editor` has written ~/.nexus/ssh/ if editors cannot resolve the host alias.")

	out.VSCodiumHint = fmt.Sprintf("code --remote ssh-remote+%s /workspace", hostAlias)
	out.CursorHint = fmt.Sprintf("cursor --remote ssh-remote+%s /workspace   (or the cursor:// deep link from `nexus workspace open-editor`)", hostAlias)
	return out
}

// ProxyJumpFromProfile extracts the proxy jump host from the profile.
func ProxyJumpFromProfile(p *profile.Profile) string {
	if p == nil || p.Host == "" {
		return ""
	}
	if p.SSHPort != 0 && p.SSHPort != 22 {
		return fmt.Sprintf("%s:%d", p.Host, p.SSHPort)
	}
	return p.Host
}

// BuildVMSSHArgs builds SSH arguments for connecting to a VM.
func BuildVMSSHArgs(proxyJump string, port int) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
	}
	if port > 0 && port != 22 {
		args = append(args, "-p", strconv.Itoa(port))
	}
	if proxyJump != "" {
		args = append(args, "-J", proxyJump)
	}
	return args
}

// ParseSSHHostPort parses a host:port string.
func ParseSSHHostPort(hostPort string) (string, int, error) {
	if h, p, err := net.SplitHostPort(hostPort); err == nil {
		port, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port %q", p)
		}
		return h, port, nil
	}
	return hostPort, 22, nil
}

// ConnectPanelConfig holds configuration for rendering the connect panel.
type ConnectPanelConfig struct {
	Width int
	Info  ConnectInstructions
}

// RenderConnectPanel renders the SSH/editor connect instructions panel.
func RenderConnectPanel(cfg ConnectPanelConfig) string {
	info := cfg.Info
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Connect (SSH / editors)"))
	if info.ProxyJump != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("ProxyJump"), info.ProxyJump)
	}
	if info.GuestTarget != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("SSH target"), info.GuestTarget)
	}
	if info.HostAlias != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("SSH config Host"), info.HostAlias)
	}
	fmt.Fprintf(&b, "\n%s\n", detailKeyStyle.Render("SSH command (copy)"))
	fmt.Fprintf(&b, "%s\n\n", detailValStyle.Render(info.SSHCommand))
	if info.ProcessShellHint != "" {
		fmt.Fprintf(&b, "%s\n%s\n\n", mutedStyle.Render(info.ProcessShellHint), detailValStyle.Render(strings.TrimSpace(info.SSHCommand)))
	}
	fmt.Fprintf(&b, "%s\n%s\n\n", detailKeyStyle.Render("VS Code"), detailValStyle.Render(info.VSCodiumHint))
	fmt.Fprintf(&b, "%s\n%s\n", detailKeyStyle.Render("Cursor"), detailValStyle.Render(info.CursorHint))
	for _, n := range info.Notes {
		if n != "" {
			fmt.Fprintf(&b, "\n%s\n", warningStyle.Render(n))
		}
	}
	return lipgloss.NewStyle().MaxWidth(cfg.Width).Render(b.String())
}
