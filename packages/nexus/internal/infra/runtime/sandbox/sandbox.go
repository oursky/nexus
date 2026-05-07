package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
)

const strictSeatbeltProfile = `(version 1)
(deny default)
(allow process*)
(deny process-exec (literal "/usr/bin/xcodebuild"))
(deny process-exec (literal "/usr/bin/xctest"))
(deny process-exec (literal "/usr/local/bin/docker"))
(deny process-exec (literal "/opt/homebrew/bin/docker"))
(allow signal (target self))
(allow file-read* file-write* file-ioctl file-map-executable)
(allow mach*)
(allow ipc*)
(allow sysctl-read)
(allow network-outbound (remote ip "localhost:*"))
(allow network-inbound (local ip "localhost:*"))
`

const relaxedSeatbeltProfile = `(version 1)
(allow default)
`

// ShellCommand builds a host process sandbox command for the configured repo.
// The returned command always executes from workDir.
func ShellCommand(shell, workDir, repoRoot string) (*exec.Cmd, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		shell = "bash"
	}
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return nil, fmt.Errorf("process sandbox: empty workdir")
	}

	relaxed := internalProcessSandboxEnabled(repoRoot)

	switch goruntime.GOOS {
	case "darwin":
		return darwinSeatbeltCommand(shell, workDir, relaxed)
	case "linux":
		return linuxBubblewrapCommand(shell, workDir, relaxed)
	default:
		return nil, fmt.Errorf("process sandbox unsupported on %s", goruntime.GOOS)
	}
}

func internalProcessSandboxEnabled(repoRoot string) bool {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		return false
	}
	if !filepath.IsAbs(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			return false
		}
		root = abs
	}
	cfg, found, err := config.LoadNexusfile(root)
	if err != nil || !found {
		return false
	}
	return cfg.Sandbox.Relaxed
}

func darwinSeatbeltCommand(shell, workDir string, relaxed bool) (*exec.Cmd, error) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return nil, fmt.Errorf("process sandbox requires sandbox-exec: %w", err)
	}

	profile := strictSeatbeltProfile
	if relaxed {
		profile = relaxedSeatbeltProfile
	}
	path, err := writeSeatbeltProfile(profile)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("sandbox-exec", "-f", path, shell)
	cmd.Dir = workDir
	return cmd, nil
}

func writeSeatbeltProfile(profile string) (string, error) {
	dir := filepath.Join(os.TempDir(), "nexus-process-sandbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create seatbelt profile dir: %w", err)
	}
	path := filepath.Join(dir, "process.sb")
	if err := os.WriteFile(path, []byte(profile), 0o644); err != nil {
		return "", fmt.Errorf("write seatbelt profile: %w", err)
	}
	return path, nil
}

func linuxBubblewrapCommand(shell, workDir string, relaxed bool) (*exec.Cmd, error) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return nil, fmt.Errorf("process sandbox requires bwrap: %w", err)
	}
	args := []string{
		"--bind", "/", "/",
		"--bind", workDir, "/workspace",
		"--proc", "/proc",
		"--dev", "/dev",
		"--die-with-parent",
		"--new-session",
		"--chdir", "/workspace",
	}
	if !relaxed {
		// Strict mode: isolate network and PID namespace.
		// Processes cannot bind to host-visible ports and cannot outlive the
		// terminal session (PID 1 exit kills the namespace).
		args = append(args, "--unshare-pid", "--unshare-net")
	}
	// Relaxed mode (sandbox.relaxed = true in Nexusfile): host network and PID
	// namespaces are shared. This allows spawning long-lived daemons (e.g. a
	// nexus daemon for self-hosted development) that bind to host ports and
	// survive after the workspace terminal session closes.
	args = append(args, shell)
	cmd := exec.Command("bwrap", args...)
	cmd.Dir = workDir
	return cmd, nil
}
