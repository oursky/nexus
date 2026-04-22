package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

func sshVMCommand() *cobra.Command {
	var check bool
	var info bool
	var diagnose bool

	cmd := &cobra.Command{
		Use:   "ssh-vm <workspace>",
		Short: "SSH into the Firecracker VM of a workspace",
		Long: `Connects to the Firecracker micro-VM running a workspace over SSH.

The engine host is used as a ProxyJump (configured via 'nexus daemon connect').
The VM must be in 'running' state, and the rootfs must include openssh-server
(installed automatically by 'nexus daemon start --setup' on a fresh system, or
by running 'nexus daemon implode && sudo nexus daemon start --setup' to rebuild).

Flags:
  --info      Print SSH connection details without connecting.
  --check     Test SSH connectivity non-interactively (runs 'whoami' in the VM).
  --diagnose  Run inside-VM checks via the workspace pty to debug failures.

Without flags an interactive SSH session is opened.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace ssh-vm: %w", err)
			}
			defer conn.Close()

			wsID, err := rpc.ResolveWorkspaceID(cmd.Context(), conn, args[0])
			if err != nil {
				return fmt.Errorf("nexus workspace ssh-vm: %w", err)
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
				return fmt.Errorf("nexus workspace ssh-vm: workspace.info: %w", err)
			}

			ws := result.Workspace
			if ws.GuestIP == "" {
				if ws.Backend != "firecracker" {
					return fmt.Errorf("nexus workspace ssh-vm: workspace %q uses backend %q — only Firecracker workspaces have a guest VM", args[0], ws.Backend)
				}
				return fmt.Errorf("nexus workspace ssh-vm: workspace %q (state: %s) has no guest IP — is it running?\n  hint: nexus workspace start %s", args[0], ws.State, args[0])
			}

			p, _ := profile.LoadDefault()
			proxyJump := buildProxyJump(p)

			if diagnose {
				mux := rpc.NewMuxConn(conn)
				return runSSHDiagnose(cmd.Context(), mux, wsID, ws.GuestIP, proxyJump, cmd)
			}

			sshTarget := "root@" + ws.GuestIP
			sshArgs := buildVMSSHArgs(proxyJump)

			if info {
				printSSHInfo(cmd, ws.GuestIP, sshTarget, proxyJump, sshArgs)
				return nil
			}

			if check {
				return runSSHCheck(sshTarget, sshArgs, cmd)
			}

			// Interactive: exec ssh so it takes over stdin/stdout/stderr.
			sshBin, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("nexus workspace ssh-vm: ssh not found in PATH: %w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Connecting to VM %s via SSH...\n", ws.GuestIP)
			allArgs := append(sshArgs, sshTarget)
			sshCmd := exec.Command(sshBin, allArgs...)
			sshCmd.Stdin = os.Stdin
			sshCmd.Stdout = os.Stdout
			sshCmd.Stderr = os.Stderr
			if err := sshCmd.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				return fmt.Errorf("nexus workspace ssh-vm: ssh: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "test SSH connectivity non-interactively and exit")
	cmd.Flags().BoolVar(&info, "info", false, "print SSH connection details and exit without connecting")
	cmd.Flags().BoolVar(&diagnose, "diagnose", false, "run inside-VM diagnostics via workspace pty to debug SSH failures")
	return cmd
}

// buildProxyJump returns the -J argument value from a daemon profile, or "" if
// running locally (no profile or no Host set).
func buildProxyJump(p *profile.Profile) string {
	if p == nil || p.Host == "" {
		return ""
	}
	if p.SSHPort != 0 && p.SSHPort != 22 {
		return fmt.Sprintf("%s:%d", p.Host, p.SSHPort)
	}
	return p.Host
}

// buildVMSSHArgs returns ssh flag arguments (without the final [user@]host).
func buildVMSSHArgs(proxyJump string) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
	}
	if proxyJump != "" {
		args = append(args, "-J", proxyJump)
	}
	return args
}

func printSSHInfo(cmd *cobra.Command, guestIP, sshTarget, proxyJump string, sshArgs []string) {
	fmt.Fprintf(cmd.OutOrStdout(), "guest-ip:    %s\n", guestIP)
	fmt.Fprintf(cmd.OutOrStdout(), "ssh-target:  %s\n", sshTarget)
	if proxyJump != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "proxy-jump:  %s\n", proxyJump)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "proxy-jump:  (none — connecting directly)\n")
	}
	fullArgs := append(append([]string{"ssh"}, sshArgs...), sshTarget)
	fmt.Fprintf(cmd.OutOrStdout(), "\nssh command:\n  %s\n", strings.Join(fullArgs, " "))
}

func runSSHCheck(sshTarget string, sshArgs []string, cmd *cobra.Command) error {
	checkArgs := append(append([]string{}, sshArgs...),
		"-o", "BatchMode=yes",
		sshTarget,
		"whoami",
	)

	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Checking SSH connectivity to %s...\n", sshTarget)
	sshCmd := exec.Command(sshBin, checkArgs...)
	var stdout, stderr strings.Builder
	sshCmd.Stdout = &stdout
	sshCmd.Stderr = &stderr

	err = sshCmd.Run()
	output := strings.TrimSpace(stdout.String())
	errOutput := strings.TrimSpace(stderr.String())

	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "FAIL: %v\n", err)
		if errOutput != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "ssh stderr: %s\n", errOutput)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\nTroubleshooting:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  1. Run --diagnose to check sshd/key status inside the VM\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  2. If sshd is not installed, rebuild the rootfs:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "       nexus daemon implode && sudo nexus daemon start --setup\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  3. Or install sshd in a running VM:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "       nexus workspace exec <ws> -- apt-get install -y openssh-server\n")
		fmt.Fprintf(cmd.OutOrStdout(), "       nexus workspace exec <ws> -- sshd\n")
		return fmt.Errorf("SSH check failed")
	}

	if output != "root" {
		fmt.Fprintf(cmd.OutOrStdout(), "FAIL: expected 'root' from whoami, got %q\n", output)
		return fmt.Errorf("SSH check failed: unexpected whoami output")
	}

	fmt.Fprintf(cmd.OutOrStdout(), "OK: SSH connection successful (whoami=%s)\n", output)
	return nil
}

// runSSHDiagnose runs a series of diagnostic commands inside the VM via the
// workspace pty, then attempts a direct TCP connection to port 22, and prints
// a concise summary with recommended next steps.
func runSSHDiagnose(ctx context.Context, conn *rpc.MuxConn, wsID, guestIP, proxyJump string, cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "=== nexus workspace ssh-vm --diagnose ===")
	fmt.Fprintf(out, "guest-ip: %s\n", guestIP)
	if proxyJump != "" {
		fmt.Fprintf(out, "proxy-jump: %s\n", proxyJump)
	}
	fmt.Fprintln(out)

	// Step 1: check reachability of port 22 from the CLIENT side.
	fmt.Fprintf(out, "[1/4] TCP port 22 reachability (client → VM)... ")
	port22Open := checkTCPPort(ctx, guestIP, proxyJump)
	if port22Open {
		fmt.Fprintln(out, "OPEN")
	} else {
		fmt.Fprintln(out, "CLOSED / TIMEOUT")
	}

	// Steps 2–4: run inside the VM via pty.
	diagScript := `
set +e
echo "=SSHD_PATH=$(which sshd 2>/dev/null || echo NOT_FOUND)"
echo "=SSHD_RUNNING=$(pgrep -x sshd >/dev/null 2>&1 && echo YES || echo NO)"
if [ -f /root/.ssh/authorized_keys ]; then
  KEYCOUNT=$(grep -c . /root/.ssh/authorized_keys 2>/dev/null || echo 0)
  echo "=AUTHORIZED_KEYS=${KEYCOUNT} line(s)"
  echo "=AUTHORIZED_KEYS_CONTENT=$(cat /root/.ssh/authorized_keys | head -3)"
else
  echo "=AUTHORIZED_KEYS=MISSING"
fi
echo "=SSHD_CONFIG=$(test -f /etc/ssh/sshd_config && echo EXISTS || echo MISSING)"
echo "=HOSTKEYS=$(ls /etc/ssh/ssh_host_*_key 2>/dev/null | tr '\n' ' ' || echo NONE)"
`

	output, execErr := runInVM(ctx, conn, wsID, diagScript)

	fmt.Fprintf(out, "[2/4] sshd binary in VM... ")
	fmt.Fprintf(out, "[3/4] sshd running... ")
	fmt.Fprintf(out, "[4/4] authorized_keys... ")
	fmt.Fprintln(out)

	if execErr != nil {
		fmt.Fprintf(out, "  ERROR running pty command in VM: %v\n", execErr)
		fmt.Fprintln(out, "  Is the workspace running? Try: nexus workspace start <ws>")
	} else {
		vals := parseKV(output)

		sshdPath := vals["SSHD_PATH"]
		sshdRunning := vals["SSHD_RUNNING"]
		authKeys := vals["AUTHORIZED_KEYS"]
		authKeysContent := vals["AUTHORIZED_KEYS_CONTENT"]
		sshdConfig := vals["SSHD_CONFIG"]
		hostKeys := vals["HOSTKEYS"]

		fmt.Fprintf(out, "[2/4] sshd binary:    %s\n", sshdPath)
		fmt.Fprintf(out, "[3/4] sshd running:   %s\n", sshdRunning)
		fmt.Fprintf(out, "[4/4] authorized_keys: %s\n", authKeys)

		if authKeysContent != "" {
			fmt.Fprintf(out, "      (first key prefix: %s...)\n", truncate(authKeysContent, 60))
		}
		if sshdConfig != "" {
			fmt.Fprintf(out, "      sshd_config: %s\n", sshdConfig)
		}
		if hostKeys != "" {
			fmt.Fprintf(out, "      host keys:   %s\n", strings.TrimSpace(hostKeys))
		}

		fmt.Fprintln(out)
		printDiagnosticAdvice(out, port22Open, sshdPath, sshdRunning, authKeys)
	}

	return nil
}

// checkTCPPort attempts a direct TCP dial to guestIP:22. If proxyJump is set,
// it attempts to TCP-dial the engine host instead (to verify basic reachability
// at the network level from the local machine).
func checkTCPPort(ctx context.Context, guestIP, proxyJump string) bool {
	target := net.JoinHostPort(guestIP, "22")
	if proxyJump != "" {
		// We can't easily reach the guest directly (it's behind the engine).
		// As a proxy, just verify we can TCP-reach the engine SSH port.
		host := proxyJump
		port := "22"
		if h, p, err := net.SplitHostPort(proxyJump); err == nil {
			host = h
			port = p
		} else {
			// proxyJump is "user@host" — strip user if present
			if idx := strings.LastIndex(proxyJump, "@"); idx >= 0 {
				host = proxyJump[idx+1:]
			}
		}
		target = net.JoinHostPort(host, port)
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", target)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func runInVM(ctx context.Context, conn *rpc.MuxConn, wsID, script string) (string, error) {
	dataCh, cancelData := conn.Subscribe("pty.data")
	defer cancelData()
	exitCh, cancelExit := conn.Subscribe("pty.exit")
	defer cancelExit()

	var session ptySessionInfo
	if err := conn.Call("pty.create", map[string]any{
		"workspaceId": wsID,
		"shell":       "/bin/sh",
		"args":        []string{"-c", script},
		"workDir":     "/root",
		"cols":        200,
		"rows":        50,
	}, &session); err != nil {
		return "", fmt.Errorf("pty.create: %w", err)
	}

	var buf strings.Builder
	timeout := time.After(30 * time.Second)
	for {
		select {
		case raw, ok := <-dataCh:
			if !ok {
				return buf.String(), nil
			}
			var p ptyDataParams
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			if p.SessionID == session.ID {
				buf.WriteString(p.Data)
			}
		case raw, ok := <-exitCh:
			if !ok {
				return buf.String(), nil
			}
			var p ptyExitParams
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			if p.SessionID == session.ID {
				return buf.String(), nil
			}
		case <-timeout:
			return buf.String(), fmt.Errorf("timeout waiting for VM command output")
		case <-ctx.Done():
			return buf.String(), ctx.Err()
		}
	}
}

// parseKV parses lines of the form "=KEY=value" from shell script output.
func parseKV(output string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "=") {
			continue
		}
		line = line[1:] // strip leading =
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		m[line[:idx]] = strings.TrimSpace(line[idx+1:])
	}
	return m
}

func printDiagnosticAdvice(out io.Writer, port22Open bool, sshdPath, sshdRunning, authKeys string) {
	fmt.Fprintln(out, "--- Recommendations ---")

	allGood := port22Open && sshdRunning == "YES" && !strings.Contains(authKeys, "MISSING")
	if allGood {
		fmt.Fprintln(out, "✓ Everything looks OK — try: nexus workspace ssh-vm <ws> --check")
		return
	}

	if sshdPath == "NOT_FOUND" || sshdPath == "" {
		fmt.Fprintln(out, "✗ sshd is NOT installed in the VM.")
		fmt.Fprintln(out, "  Quick fix (install into running VM):")
		fmt.Fprintln(out, "    nexus workspace exec <ws> -- bash -c 'apt-get install -y openssh-server'")
		fmt.Fprintln(out, "    nexus workspace exec <ws> -- /usr/sbin/sshd")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Permanent fix (rebuild rootfs — requires sudo):")
		fmt.Fprintln(out, "    nexus daemon implode && sudo nexus daemon start --setup")
		return
	}

	if sshdRunning != "YES" {
		fmt.Fprintf(out, "✗ sshd is installed (%s) but NOT running.\n", sshdPath)
		fmt.Fprintln(out, "  Start it:")
		fmt.Fprintln(out, "    nexus workspace exec <ws> -- /usr/sbin/sshd")
		fmt.Fprintln(out)
	}

	if strings.Contains(authKeys, "MISSING") || authKeys == "0 line(s)" {
		fmt.Fprintln(out, "✗ /root/.ssh/authorized_keys is missing or empty.")
		fmt.Fprintln(out, "  The VM gets your host public keys injected via the config drive.")
		fmt.Fprintln(out, "  Ensure you have at least one key in ~/.ssh/ on the engine host")
		fmt.Fprintln(out, "  (id_ed25519.pub, id_rsa.pub, id_ecdsa.pub, or any other *.pub file).")
		fmt.Fprintln(out, "  Then restart the workspace so the config drive is rebuilt:")
		fmt.Fprintln(out, "    nexus workspace stop <ws> && nexus workspace start <ws>")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Or inject your key directly (for a quick test):")
		fmt.Fprintln(out, "    cat ~/.ssh/id_ed25519.pub | nexus workspace exec <ws> -- tee -a /root/.ssh/authorized_keys")
	}

	if !port22Open {
		fmt.Fprintln(out, "✗ TCP port 22 is not reachable from this machine.")
		fmt.Fprintln(out, "  Check that the engine host is reachable and the SSH port is not blocked.")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
