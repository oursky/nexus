package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

func connectCommand() *cobra.Command {
	var port int
	var sshPort int
	var verbose bool

	cmd := &cobra.Command{
		Use:   "connect <host>",
		Short: "Connect to a remote daemon and save profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := args[0]

			token, err := fetchRemoteToken(host, sshPort, verbose)
			if err != nil {
				return fmt.Errorf("fetch remote token: %w", err)
			}

			p := &profile.Profile{
				Name:    "default",
				Host:    host,
				Port:    port,
				Token:   token,
				SSHPort: sshPort,
			}
			if err := profile.SaveDefault(p); err != nil {
				return fmt.Errorf("save profile: %w", err)
			}
			if err := ensureRemoteEditorSSHIncludes(); err != nil {
				return fmt.Errorf("update local SSH config: %w", err)
			}

			conn, err := rpc.EnsureDaemonVerbose(verbose)
			if err != nil {
				_ = profile.DeleteDefault()
				return fmt.Errorf("connection test failed: %w", err)
			}
			conn.Close()

			fmt.Fprintf(cmd.OutOrStdout(), "Connected to %s (port %d)\n", host, port)
			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 7777, "remote daemon port")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 0, "SSH port (default: 22)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print SSH commands and enable verbose SSH output")
	return cmd
}

func fetchRemoteToken(host string, sshPort int, verbose bool) (string, error) {
	// Use `nexus daemon token` on the remote host so the token is read from
	// Use the full installed path because non-interactive SSH sessions do not
	// source shell profiles, so ~/.local/bin is not in $PATH.
	args := []string{host, "$HOME/.local/bin/nexus", "daemon", "token"}
	if sshPort > 0 && sshPort != 22 {
		args = append([]string{"-p", fmt.Sprintf("%d", sshPort)}, args...)
	}
	if verbose {
		args = append([]string{"-v"}, args...)
		fmt.Fprintf(os.Stderr, "[nexus] fetch token: ssh %s\n", strings.Join(args, " "))
	}
	out, err := exec.Command("ssh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("ssh %s nexus daemon token: %w", host, err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("no token found on remote host (is the daemon started?)")
	}
	return token, nil
}

func ensureRemoteEditorSSHIncludes() error {
	includeLines := []string{
		"Include ~/.nexus/ssh/*.ssh.config",
		"Include ~/Library/Containers/com.oursky.nexus/Data/.nexus/ssh/*.ssh.config",
		"Include ~/Library/Containers/com.oursky.nexus.local/Data/.nexus/ssh/*.ssh.config",
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return fmt.Errorf("resolve home directory: %w", err)
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

	newBody := regexp.MustCompile(`(?m)^# nexus VM remote-editor.*\n`).ReplaceAllString(bodyStr, "")
	for _, includeLine := range includeLines {
		pattern := regexp.QuoteMeta(includeLine)
		newBody = regexp.MustCompile(`(?m)^`+pattern+`\n?`).ReplaceAllString(newBody, "")
	}
	prefix := "# nexus VM remote-editor (managed by Nexus — must be first)\n" + strings.Join(includeLines, "\n") + "\n\n"
	return os.WriteFile(cfgPath, []byte(prefix+newBody), 0o600)
}
