package workspace

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

func openEditorCommand() *cobra.Command {
	var app string
	var checkOnly bool
	var skipCheck bool

	cmd := &cobra.Command{
		Use:   "open-editor <workspace>",
		Short: "Open a workspace VM in Cursor or VS Code via Remote SSH",
		Long: `Writes the SSH host-alias config to ~/.nexus/ssh/, verifies SSH connectivity
from this machine (direct to the VM when the daemon is local, otherwise
Mac → engine (ProxyJump) → VM), then opens the editor deep-link.

The workspace must be running with a libkrun backend.

Flags:
  --app cursor|vscode   Editor to open (default: cursor)
  --check               Only test SSH connectivity, do not open the editor.
  --skip-check          Skip SSH test and open the editor immediately.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenEditor(cmd, args[0], app, checkOnly, skipCheck)
		},
	}

	cmd.Flags().StringVar(&app, "app", "cursor", "editor to open: cursor or vscode")
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only test SSH, do not open editor")
	cmd.Flags().BoolVar(&skipCheck, "skip-check", false, "skip SSH test, open editor immediately")
	return cmd
}

func runOpenEditor(cmd *cobra.Command, wsNameOrID, app string, checkOnly, skipCheck bool) error {
	conn, err := rpc.EnsureDaemon()
	if err != nil {
		return fmt.Errorf("open-editor: %w", err)
	}
	defer conn.Close()

	wsID, err := rpc.ResolveWorkspaceID(cmd.Context(), conn, wsNameOrID)
	if err != nil {
		return fmt.Errorf("open-editor: %w", err)
	}

	var result struct {
		Workspace struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Backend string `json:"backend"`
			GuestIP string `json:"guestIp"`
		} `json:"workspace"`
	}
	if err := rpc.Do(conn, "workspace.info", map[string]any{"id": wsID}, &result); err != nil {
		return fmt.Errorf("open-editor: workspace.info: %w", err)
	}

	ws := result.Workspace
	if ws.GuestIP == "" {
		if !domainws.UsesGuestVM(ws.Backend) {
			return fmt.Errorf("open-editor: workspace %q uses backend %q — only libkrun workspaces support VM remote-editor access", wsNameOrID, ws.Backend)
		}
		return fmt.Errorf("open-editor: workspace %q (state: %s) has no guest IP — is it running?\n  hint: nexus workspace start %s", wsNameOrID, ws.State, wsNameOrID)
	}

	direct, proxyJump, jumpPort, jumpIdentity, err := resolveOpenEditorSSH(ws.GuestIP)
	if err != nil {
		return err
	}

	hostAlias := "nexus-vm-" + ws.ID

	if err := writeNexusSSHConfig(hostAlias, ws.GuestIP, direct, proxyJump, jumpPort, jumpIdentity); err != nil {
		return fmt.Errorf("open-editor: writing SSH config: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote ~/.nexus/ssh/%s.ssh.config\n", hostAlias)

	if !skipCheck {
		sshTarget := ws.GuestIP
		if h, p := parseGuestIPPort(ws.GuestIP); p != "22" {
			sshTarget = fmt.Sprintf("%s (port %s)", h, p)
		}
		if direct {
			fmt.Fprintf(cmd.OutOrStdout(), "checking SSH: %s ...\n", sshTarget)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "checking SSH: Mac → %s → %s ...\n", proxyJump, sshTarget)
		}
		ok, detail := runLocalSSHCheck(ws.GuestIP, direct, proxyJump, jumpPort, jumpIdentity)
		if !ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "FAIL: %s\n", detail)
			fmt.Fprintf(cmd.ErrOrStderr(), "\nTroubleshoot:\n")
			fmt.Fprintf(cmd.ErrOrStderr(), "  nexus workspace ssh-vm %s --diagnose\n", wsNameOrID)
			return fmt.Errorf("SSH check failed — editor not opened")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "OK: SSH connection successful\n")
	}

	if checkOnly {
		return nil
	}

	ensureRemoteDir(ws.GuestIP, direct, proxyJump, jumpPort, jumpIdentity, "/workspace/.cursor-server")
	ensureRemoteDir(ws.GuestIP, direct, proxyJump, jumpPort, jumpIdentity, "/workspace/.vscode-server")

	editorApp := strings.ToLower(strings.TrimSpace(app))
	if editorApp == "" {
		editorApp = "cursor"
	}
	editorURL := fmt.Sprintf("%s://vscode-remote/ssh-remote+%s/workspace", editorApp, hostAlias)
	fmt.Fprintf(cmd.OutOrStdout(), "opening %s: %s\n", editorApp, editorURL)
	if err := openURL(editorURL); err != nil {
		return fmt.Errorf("could not open %s (is it installed?): %w", editorApp, err)
	}
	return nil
}

// guestSSHIsDirect is true when the guest SSH address is on loopback on this
// machine (local libkrun / gvproxy port-forward). In that case the client must
// connect directly and must not use a ProxyCommand to a remote engine.
func guestSSHIsDirect(guestIP string) bool {
	host := strings.TrimSpace(guestIP)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return isLoopbackSSHHost(host)
}

func isLoopbackSSHHost(host string) bool {
	localhostNames := []string{"localhost", "127.0.0.1", "::1", "0.0.0.0"}
	lower := strings.ToLower(strings.TrimSpace(host))
	for _, name := range localhostNames {
		if lower == name {
			return true
		}
	}
	return false
}

func resolveOpenEditorSSH(guestIP string) (direct bool, proxyJump string, jumpPort int, jumpIdentity string, err error) {
	proxyJump = strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_HOST"))
	jumpPort = 0
	if rawPort := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_PORT")); rawPort != "" {
		parsedPort, perr := strconv.Atoi(rawPort)
		if perr != nil || parsedPort <= 0 {
			return false, "", 0, "", fmt.Errorf("open-editor: invalid NEXUS_DAEMON_SSH_PORT=%q", rawPort)
		}
		jumpPort = parsedPort
	}
	jumpIdentity = strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_IDENTITY"))

	if guestSSHIsDirect(guestIP) {
		if jumpIdentity == "" {
			if p, perr := profile.LoadDefault(); perr == nil && p != nil && p.SSHIdentityFile != "" {
				jumpIdentity = p.SSHIdentityFile
			}
		}
		return true, "", 0, jumpIdentity, nil
	}

	if proxyJump == "" {
		p, perr := profile.LoadDefault()
		if perr != nil {
			return false, "", 0, "", fmt.Errorf("open-editor: %w", perr)
		}
		proxyJump = buildProxyJump(p)
		if jumpPort == 0 && p != nil && p.SSHPort > 0 {
			jumpPort = p.SSHPort
		}
	}
	if proxyJump == "" {
		return false, "", 0, "", fmt.Errorf("open-editor: no engine SSH host configured (set NEXUS_DAEMON_SSH_HOST or run 'nexus daemon connect' first)")
	}
	return false, proxyJump, jumpPort, jumpIdentity, nil
}

// writeNexusSSHConfig writes ~/.nexus/ssh/<hostAlias>.ssh.config and ensures
// ~/.ssh/config contains an Include for that directory.
// parseGuestIPPort splits a "host:port" string into (host, port).
// If guestIP has no ":", it returns (guestIP, "22").
func parseGuestIPPort(guestIP string) (host, port string) {
	if idx := strings.LastIndex(guestIP, ":"); idx >= 0 {
		return guestIP[:idx], guestIP[idx+1:]
	}
	return guestIP, "22"
}

func writeNexusSSHConfig(hostAlias, guestIP string, direct bool, proxyJump string, jumpPort int, jumpIdentity string) error {
	homeDir, err := sshConfigHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(homeDir, ".nexus", "ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// guestIP may be "host:port" (slirp4netns port-forward) or a plain IP.
	host, port := parseGuestIPPort(guestIP)
	lines := []string{
		"# Generated by nexus workspace open-editor (overwritten on each open).",
		"Host " + hostAlias,
		"  HostName " + host,
		"  User root",
		"  UserKnownHostsFile /dev/null",
		"  SetEnv VSCODE_AGENT_FOLDER=/workspace/.vscode-server CURSOR_AGENT_FOLDER=/workspace/.cursor-server",
	}
	if direct {
		lines = append(lines,
			"  StrictHostKeyChecking no",
			"  GlobalKnownHostsFile /dev/null",
		)
		if jumpIdentity != "" {
			lines = append(lines, "  IdentityFile "+jumpIdentity)
		}
	} else {
		jumpArgs := buildJumpSSHBaseArgs(jumpPort, jumpIdentity)
		proxyCommand := "ssh " + strings.Join(append(jumpArgs, "-W", "%h:%p", proxyJump), " ")
		lines = append(lines,
			"  ProxyCommand "+proxyCommand,
			"  StrictHostKeyChecking accept-new",
		)
	}
	if port != "22" {
		lines = append(lines, "  Port "+port)
	}
	content := strings.Join(lines, "\n") + "\n"
	path := filepath.Join(dir, hostAlias+".ssh.config")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	return ensureSSHInclude()
}

// ensureSSHInclude ensures `Include ~/.nexus/ssh/*.ssh.config` is the very first
// line of ~/.ssh/config. It must come before any Host/Match blocks so that nexus
// VM host aliases are resolved before the catch-all `Host *` sets User/HostName
// defaults (SSH first-match-wins semantics).
func ensureSSHInclude() error {
	includeLines := []string{
		"Include ~/.nexus/ssh/*.ssh.config",
		"Include ~/Library/Containers/com.oursky.nexus/Data/.nexus/ssh/*.ssh.config",
		"Include ~/Library/Containers/com.oursky.nexus.local/Data/.nexus/ssh/*.ssh.config",
	}
	homeDir, err := sshConfigHomeDir()
	if err != nil {
		return err
	}
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	cfgPath := filepath.Join(sshDir, "config")
	body, _ := os.ReadFile(cfgPath)
	bodyStr := string(body)
	hasAllIncludes := true
	for _, includeLine := range includeLines {
		if !strings.Contains(bodyStr, includeLine) {
			hasAllIncludes = false
			break
		}
	}

	if hasAllIncludes {
		// Already present — ensure the first managed Include line is at the top.
		lines := strings.SplitAfter(bodyStr, "\n")
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if trimmed == includeLines[0] {
				return nil
			}
			break
		}
	}

	// Remove any previous managed block and any existing copies of our include lines.
	newBody := regexp.MustCompile(`(?m)^# nexus VM remote-editor.*\n`).ReplaceAllString(bodyStr, "")
	for _, includeLine := range includeLines {
		pattern := regexp.QuoteMeta(includeLine)
		newBody = regexp.MustCompile(`(?m)^`+pattern+`\n?`).ReplaceAllString(newBody, "")
	}

	prefix := "# nexus VM remote-editor (managed by Nexus — must be first)\n" + strings.Join(includeLines, "\n") + "\n\n"
	newBody = prefix + newBody
	return os.WriteFile(cfgPath, []byte(newBody), 0o600)
}

func sshConfigHomeDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv("NEXUS_REAL_HOME")); override != "" {
		return override, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return "", fmt.Errorf("resolve home directory for SSH config: %w", err)
	}
	return homeDir, nil
}

// runLocalSSHCheck runs `ssh ... root@host whoami` from the local machine,
// optionally via a ProxyCommand to the engine host, using explicit -o flags so
// $SHELL and ~/.ssh/known_hosts cannot interfere with the test.
func runLocalSSHCheck(guestIP string, direct bool, proxyJump string, jumpPort int, jumpIdentity string) (ok bool, detail string) {
	// guestIP may be "host:port" (slirp4netns port-forward) or a plain IP.
	host, port := parseGuestIPPort(guestIP)

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-p", port,
	}
	if !direct {
		proxyCmd := buildProxyCommand(proxyJump, jumpPort, jumpIdentity)
		args = append(args, "-o", "ProxyCommand="+proxyCmd)
	} else if jumpIdentity != "" {
		args = append(args, "-i", jumpIdentity)
	}
	args = append(args, "root@"+host, "whoami")

	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return false, "ssh not found in PATH"
	}
	c := exec.Command(sshBin, args...)
	// Override SHELL so ssh uses /bin/sh for internal subcommands, not the
	// user's custom shell (e.g. fish from a nix profile).
	env := os.Environ()
	for i, e := range env {
		if strings.HasPrefix(e, "SHELL=") {
			env[i] = "SHELL=/bin/sh"
			goto envSet
		}
	}
	env = append(env, "SHELL=/bin/sh")
envSet:
	c.Env = env

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return false, msg
	}
	return true, ""
}

// ensureRemoteDir runs `mkdir -p <dir>` on the remote VM via SSH.
// Uses the same no-hostkey-check options as the connectivity check.
// Best-effort: logs a warning on failure but does not abort the open.
func ensureRemoteDir(guestIP string, direct bool, proxyJump string, jumpPort int, jumpIdentity string, dir string) {
	host, port := parseGuestIPPort(guestIP)
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return
	}
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-p", port,
	}
	if !direct {
		proxyCmd := buildProxyCommand(proxyJump, jumpPort, jumpIdentity)
		args = append(args, "-o", "ProxyCommand="+proxyCmd)
	} else if jumpIdentity != "" {
		args = append(args, "-i", jumpIdentity)
	}
	args = append(args, "root@"+host, "mkdir", "-p", dir)
	c := exec.Command(sshBin, args...)
	env := os.Environ()
	for i, e := range env {
		if strings.HasPrefix(e, "SHELL=") {
			env[i] = "SHELL=/bin/sh"
			goto envSet
		}
	}
	env = append(env, "SHELL=/bin/sh")
envSet:
	c.Env = env
	_ = c.Run() // best-effort
}

func buildProxyCommand(proxyJump string, jumpPort int, jumpIdentity string) string {
	base := append(buildJumpSSHBaseArgs(jumpPort, jumpIdentity), "-W", "%h:%p", proxyJump)
	return "ssh " + strings.Join(base, " ")
}

func buildJumpSSHBaseArgs(jumpPort int, jumpIdentity string) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "LogLevel=ERROR",
	}
	if jumpPort > 0 {
		args = append(args, "-p", strconv.Itoa(jumpPort))
	}
	if jumpIdentity != "" {
		args = append(args, "-i", jumpIdentity)
	}
	return args
}

// openURL opens a URL using the OS default handler.
func openURL(url string) error {
	var bin string
	switch runtime.GOOS {
	case "darwin":
		bin = "open"
	case "linux":
		bin = "xdg-open"
	default:
		fmt.Println(url)
		return nil
	}
	return exec.Command(bin, url).Run()
}
