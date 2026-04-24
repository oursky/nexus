//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// privilegeMode describes how privileged steps will be executed.
type privilegeMode int

const (
	// privilegeModeRoot: EUID == 0, run commands directly.
	privilegeModeRoot privilegeMode = iota
	// privilegeModeSudoN: passwordless sudo available (CI); use sudo -n.
	privilegeModeSudoN
	// privilegeModeInteractive: stdin is a TTY; run sudo interactively.
	privilegeModeInteractive
	// privilegeModeManual: no privilege path — print commands for the user.
	privilegeModeManual
)

// setupPrivilegeModeOverride, when setupPrivilegeModeOverrideEnabled is true,
// overrides the auto-detected privilege mode.  Tests flip the enabled flag.
var setupPrivilegeModeOverride privilegeMode
var setupPrivilegeModeOverrideEnabled bool

// setupRunScriptFn runs the privileged setup bash script.  Overridable in tests.
var setupRunScriptFn = runSetupScript

// setupVerifyFn verifies that the daemon setup completed correctly.  Overridable in tests.
var setupVerifyFn = verifyDaemonSetup

// errKVMGroupRefreshNeeded indicates setup is complete but current session
// still lacks active /dev/kvm group access.
var errKVMGroupRefreshNeeded = errors.New("kvm group refresh needed")

const setupKVMGroupReexecEnv = "NEXUS_SETUP_KVM_GROUP_REEXEC"

// setupKVMGroupReexecFn re-runs the current nexus command under `sg kvm` so
// group membership takes effect without requiring a full logout/login cycle.
var setupKVMGroupReexecFn = func(commandPath string) error {
	parts := make([]string, 0, len(os.Args))
	parts = append(parts, shellQuote(commandPath))
	for _, arg := range os.Args[1:] {
		parts = append(parts, shellQuote(arg))
	}
	cmd := exec.Command("sg", "kvm", "-c", strings.Join(parts, " "))
	cmd.Env = append(os.Environ(), setupKVMGroupReexecEnv+"=1")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// detectPrivilegeMode returns the appropriate privilege escalation strategy.
func detectPrivilegeMode(isRoot, sudoNOK, stdinIsTTY bool) privilegeMode {
	if isRoot {
		return privilegeModeRoot
	}
	if sudoNOK {
		return privilegeModeSudoN
	}
	if stdinIsTTY {
		return privilegeModeInteractive
	}
	return privilegeModeManual
}

// resolvePrivilegeMode probes the current runtime to pick the best strategy.
func resolvePrivilegeMode() privilegeMode {
	if setupPrivilegeModeOverrideEnabled {
		return setupPrivilegeModeOverride
	}
	isRoot := os.Geteuid() == 0
	sudoNOK := exec.Command("sudo", "-n", "true").Run() == nil
	stdinIsTTY := isTerminal(os.Stdin)
	return detectPrivilegeMode(isRoot, sudoNOK, stdinIsTTY)
}

// isTerminal returns true when f refers to a terminal device.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// errNeedsManual is returned when a privileged step requires manual intervention.
var errNeedsManual = errors.New("manual privileged command required")

// setupCommandPath returns the command path users should run with sudo.
func setupCommandPath() string {
	if exe, err := os.Executable(); err == nil {
		exe = strings.TrimSpace(exe)
		if exe != "" {
			return exe
		}
	}
	if len(os.Args) > 0 {
		arg0 := strings.TrimSpace(os.Args[0])
		if arg0 != "" {
			if filepath.IsAbs(arg0) {
				return arg0
			}
			if lp, err := exec.LookPath(arg0); err == nil {
				return lp
			}
			return arg0
		}
	}
	return "nexus"
}

// runSetupScript executes the given bash script content under the appropriate
// privilege mode.
func runSetupScript(mode privilegeMode, script string) error {
	switch mode {
	case privilegeModeRoot:
		cmd := exec.Command("bash", "-s")
		cmd.Stdin = strings.NewReader(script)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	case privilegeModeSudoN:
		cmd := exec.Command("sudo", "-n", "bash", "-s")
		cmd.Stdin = strings.NewReader(script)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	case privilegeModeInteractive:
		cmd := exec.Command("sudo", "bash", "-s")
		cmd.Stdin = strings.NewReader(script)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	case privilegeModeManual:
		return errNeedsManual
	default:
		return fmt.Errorf("unknown privilege mode: %d", mode)
	}
}

// resolveInstallBinDir returns the user-local bin directory.
func resolveInstallBinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	// Honour SUDO_USER so the directory belongs to the invoking user, not root.
	if su := strings.TrimSpace(os.Getenv("SUDO_USER")); su != "" {
		if out, err := exec.Command("getent", "passwd", su).Output(); err == nil {
			fields := strings.SplitN(strings.TrimSpace(string(out)), ":", 7)
			if len(fields) >= 6 && fields[5] != "" {
				home = fields[5]
			}
		}
	}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", binDir, err)
	}
	return binDir, nil
}

// verifyDaemonSetup checks that the daemon setup (libkrun) completed successfully.
func verifyDaemonSetup() error {
	home, _ := os.UserHomeDir()
	libDir := filepath.Join(home, ".local", "share", "nexus", "lib")
	soPath := filepath.Join(libDir, "libkrun.so.1")
	if _, err := os.Stat(soPath); err != nil {
		return fmt.Errorf("libkrun.so.1 not found at %s: %w", soPath, err)
	}

	// Check KVM access.
	fd, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w: current session lacks read/write access to /dev/kvm", errKVMGroupRefreshNeeded)
		}
		return fmt.Errorf("unable to open /dev/kvm: %w", err)
	}
	_ = fd.Close()
	return nil
}

// moduleRoot returns the Go module root directory of the nexus package.
func moduleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}
