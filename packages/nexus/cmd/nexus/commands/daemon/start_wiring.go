package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// launchDaemonBackground re-execs the current binary with NEXUS_DAEMON_FOREGROUND=1,
// detached from the terminal (new session, stdout/stderr → log file).
func launchDaemonBackground(out io.Writer, socketPath string, jsonOut bool, readyTimeout time.Duration) error {
	emitPhase := func(status, message string) {
		if jsonOut {
			fmt.Fprintf(out, `{"phase":"daemon-launch","status":%q,"message":%q}`+"\n", status, message)
		} else {
			fmt.Fprintf(out, "daemon-launch: %s: %s\n", status, message)
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemon start: resolve executable: %w", err)
	}

	logDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("daemon start: create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("daemon start: open log: %w", err)
	}
	defer logFile.Close()

	child := exec.Command(exe, os.Args[1:]...)
	child.Env = append(os.Environ(), daemonForegroundEnv+"=1")
	child.Stdout = logFile
	child.Stderr = logFile
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := child.Start(); err != nil {
		return fmt.Errorf("daemon start: launch background process: %w", err)
	}

	emitPhase("start", fmt.Sprintf("background process pid=%d", child.Process.Pid))

	// Wait in background so ProcessState is populated if the child exits early.
	go func() {
		_ = child.Wait()
	}()

	// Poll for socket readiness.
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if _, statErr := os.Stat(socketPath); statErr == nil {
			emitPhase("ok", fmt.Sprintf("pid=%d log=%s", child.Process.Pid, logPath))
			return nil
		}
		if child.ProcessState != nil {
			// Dump the daemon log so we can see why it exited.
			if logData, err := os.ReadFile(logPath); err == nil && len(logData) > 0 {
				lines := strings.Split(string(logData), "\n")
				if len(lines) > 50 {
					lines = lines[len(lines)-50:]
				}
				emitPhase("error", fmt.Sprintf("daemon exited (exit %d) — log tail:\n%s",
					child.ProcessState.ExitCode(), strings.Join(lines, "\n")))
			}
			return fmt.Errorf("daemon exited unexpectedly (exit %d) — check log: %s",
				child.ProcessState.ExitCode(), logPath)
		}
	}
	return fmt.Errorf("daemon did not become ready within %s — check log: %s", readyTimeout, logPath)
}

// ensureGuestAgent writes the guest-agent binary into the rootfs image at
// /usr/local/bin/nexus-guest-agent. Injection is skipped when the
// binary hash matches the stored hash (avoids slow debugfs round-trips).
func ensureGuestAgent(rootfsPath string) error {
	rootfsPath = strings.TrimSpace(rootfsPath)
	if rootfsPath == "" {
		return fmt.Errorf("rootfs path is required")
	}
	if _, err := os.Stat(rootfsPath); err != nil {
		return fmt.Errorf("rootfs not found at %q: %w", rootfsPath, err)
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return fmt.Errorf("debugfs is required to refresh guest agent payload: %w", err)
	}

	agentPath, err := resolveGuestAgentBinary()
	if err != nil {
		return err
	}

	hashFile := agentHashFile()
	agentHash, hashErr := sha256HexFile(agentPath)
	if hashErr == nil {
		if stored, readErr := os.ReadFile(hashFile); readErr == nil &&
			strings.TrimSpace(string(stored)) == agentHash {
			return nil
		}
	}

	// Repair any dirty journal before writing with debugfs.
	_ = exec.Command("e2fsck", "-f", "-y", rootfsPath).Run()

	for _, cmd := range []string{
		"mkdir /usr/local",
		"mkdir /usr/local/bin",
		"mkdir /workspace",
		"rm /usr/local/bin/nexus-guest-agent",
	} {
		_ = exec.Command("debugfs", "-w", "-R", cmd, rootfsPath).Run()
	}

	if out, err := exec.Command("debugfs", "-w", "-R", fmt.Sprintf("write %s /usr/local/bin/nexus-guest-agent", agentPath), rootfsPath).CombinedOutput(); err != nil {
		return fmt.Errorf("write guest agent into rootfs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("debugfs", "-w", "-R", "sif /usr/local/bin/nexus-guest-agent mode 0100755", rootfsPath).CombinedOutput(); err != nil {
		return fmt.Errorf("set mode on guest agent in rootfs: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if hashErr == nil {
		if dir := filepath.Dir(hashFile); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		_ = os.WriteFile(hashFile, []byte(agentHash+"\n"), 0o644)
	}
	return nil
}

// sha256HexFile returns the hex-encoded SHA-256 digest of the file at path.
func sha256HexFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func resolveGuestAgentBinary() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("NEXUS_GUEST_AGENT_BIN")); raw != "" {
		if _, err := os.Stat(raw); err != nil {
			return "", fmt.Errorf("NEXUS_GUEST_AGENT_BIN=%s: %w", raw, err)
		}
		return raw, nil
	}
	if EmbeddedAgentFn == nil {
		return "", fmt.Errorf("guest agent binary is not embedded in this build (linux/amd64 or linux/arm64 required)")
	}
	data := EmbeddedAgentFn()
	if len(data) == 0 {
		return "", fmt.Errorf("embedded guest agent is empty (build error)")
	}

	home, _ := os.UserHomeDir()
	dest := filepath.Join(home, ".local", "bin", "nexus-guest-agent")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}

	newHash := sha256.Sum256(data)
	needsWrite := true
	if existing, err := os.ReadFile(dest); err == nil {
		existingHash := sha256.Sum256(existing)
		needsWrite = existingHash != newHash
	}
	if needsWrite {
		tmp := dest + ".tmp"
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return "", fmt.Errorf("write embedded agent: %w", err)
		}
		if err := os.Rename(tmp, dest); err != nil {
			return "", fmt.Errorf("install embedded agent: %w", err)
		}
	}
	return dest, nil
}
