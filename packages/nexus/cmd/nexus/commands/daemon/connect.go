package daemon

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

func connectCommand() *cobra.Command {
	var port int
	var sshPort int

	cmd := &cobra.Command{
		Use:   "connect <host>",
		Short: "Connect to a remote daemon and save profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := args[0]

			token, err := fetchRemoteToken(host, sshPort)
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

			conn, err := rpc.EnsureDaemon()
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
	return cmd
}

func fetchRemoteToken(host string, sshPort int) (string, error) {
	// Use `nexus daemon token` on the remote host so the token is read from
	// Use the full installed path because non-interactive SSH sessions do not
	// source shell profiles, so ~/.local/bin is not in $PATH.
	args := []string{host, "$HOME/.local/bin/nexus", "daemon", "token"}
	if sshPort > 0 && sshPort != 22 {
		args = append([]string{"-p", fmt.Sprintf("%d", sshPort)}, args...)
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
