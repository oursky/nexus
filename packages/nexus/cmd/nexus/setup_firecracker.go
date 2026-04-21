//go:build linux

package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed scripts/firecracker-setup.sh
var firecrackerSetupScript []byte

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

// setupExtractFirecrackerFn extracts the embedded Firecracker binary to a
// temporary file and returns its path. Overridable in tests.
var setupExtractFirecrackerFn = func() (string, error) {
	if len(embeddedFirecracker) == 0 {
		return "", fmt.Errorf("firecracker binary is not embedded in this build (linux/amd64 or linux/arm64 required)")
	}
	tmp, err := os.CreateTemp("", "nexus-firecracker-*")
	if err != nil {
		return "", fmt.Errorf("create temp file for firecracker: %w", err)
	}
	dest := tmp.Name()
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file for firecracker: %w", err)
	}
	if err := os.WriteFile(dest, embeddedFirecracker, 0o755); err != nil {
		return "", fmt.Errorf("extract embedded firecracker: %w", err)
	}
	return dest, nil
}

// setupBuildTapHelperFn builds or extracts the nexus-tap-helper binary and
// returns its path.  Overridable in tests.
//
// Preference order:
//  1. Extract from embeddedTapHelper (set at build time via //go:embed).
//  2. Build from Go source if the module root can be located (dev fallback).
var setupBuildTapHelperFn = func() (string, error) {
	tmp, err := os.CreateTemp("", "nexus-tap-helper-*")
	if err != nil {
		return "", fmt.Errorf("create temp file for nexus-tap-helper: %w", err)
	}
	dest := tmp.Name()
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file for nexus-tap-helper: %w", err)
	}

	// Fast path: extract the binary that was embedded at build time.
	if len(embeddedTapHelper) > 0 {
		if err := os.WriteFile(dest, embeddedTapHelper, 0o755); err != nil {
			return "", fmt.Errorf("extract embedded nexus-tap-helper: %w", err)
		}
		return dest, nil
	}

	// Fallback: build from source (works only when running from the module
	// root, e.g. during `go run ./cmd/nexus` in a dev checkout).
	root := moduleRoot()
	localSrc := root + "/cmd/nexus-tap-helper"
	if _, err := os.Stat(localSrc); err != nil {
		return "", fmt.Errorf(
			"nexus-tap-helper not embedded and source not found at %s\n"+
				"Rebuild nexus with: cd packages/nexus && go generate ./cmd/nexus && go build ./cmd/nexus",
			localSrc,
		)
	}
	cmd := exec.Command("go", "build", "-o", dest, "./cmd/nexus-tap-helper/")
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build nexus-tap-helper: %w", err)
	}
	return dest, nil
}

// setupExtractAgentFn extracts the nexus-firecracker-agent binary and returns
// its path.  Overridable in tests.
//
// Preference order:
//  1. Extract from embeddedAgent (set at build time via //go:embed).
//  2. Build from Go source if the module root can be located (dev fallback).
var setupExtractAgentFn = func() (string, error) {
	tmp, err := os.CreateTemp("", "nexus-firecracker-agent-*")
	if err != nil {
		return "", fmt.Errorf("create temp file for nexus-firecracker-agent: %w", err)
	}
	dest := tmp.Name()
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file for nexus-firecracker-agent: %w", err)
	}

	// Fast path: extract the binary that was embedded at build time.
	if len(embeddedAgent) > 0 {
		if err := os.WriteFile(dest, embeddedAgent, 0o755); err != nil {
			return "", fmt.Errorf("extract embedded nexus-firecracker-agent: %w", err)
		}
		return dest, nil
	}

	// Fallback: build from source (works only when running from the module
	// root, e.g. during `go run ./cmd/nexus` in a dev checkout).
	root := moduleRoot()
	localSrc := root + "/cmd/nexus-firecracker-agent"
	if _, err := os.Stat(localSrc); err != nil {
		return "", fmt.Errorf(
			"nexus-firecracker-agent not embedded and source not found at %s\n"+
				"Rebuild nexus with: cd packages/nexus && go generate ./cmd/nexus && go build ./cmd/nexus",
			localSrc,
		)
	}
	cmd := exec.Command("go", "build", "-o", dest, "./cmd/nexus-firecracker-agent/")
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build nexus-firecracker-agent: %w", err)
	}
	return dest, nil
}

// setupRunScriptFn runs the privileged setup bash script.  Overridable in
// tests.
var setupRunScriptFn = runSetupScript

// setupVerifyFn verifies that the setup completed correctly.  Overridable in
// tests.
var setupVerifyFn = verifyFirecrackerSetup

// setupSudoReexecFn reruns the current nexus command under sudo so users can
// complete privileged setup steps in one command invocation. Overridable in tests.
var setupSudoReexecFn = func(commandPath string) error {
	args := append([]string{commandPath}, os.Args[1:]...)
	cmd := exec.Command("sudo", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

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

// detectPrivilegeMode returns the appropriate privilege escalation strategy
// based on the three boolean inputs.
//
//   - isRoot:      os.Geteuid() == 0
//   - sudoNOK:     `sudo -n true` exits 0
//   - stdinIsTTY:  os.Stdin is a TTY
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

// errNeedsManual is returned when a privileged step requires manual
// intervention.
var errNeedsManual = errors.New("manual privileged command required")

// moduleRoot returns the Go module root directory of the nexus package.
// It resolves relative to the binary or falls back to the working directory.
func moduleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

// vmAssetsDir is the directory where VM assets (kernel, rootfs) are stored.
const vmAssetsDir = "/var/lib/nexus"

// DefaultVMKernelPath is the default kernel path used by nexus daemon start.
const DefaultVMKernelPath = vmAssetsDir + "/vmlinux.bin"

// DefaultVMRootfsPath is the default rootfs path used by nexus daemon start.
const DefaultVMRootfsPath = vmAssetsDir + "/rootfs.ext4"

// buildSetupScript prepends variable-export lines to the embedded setup script
// so the script can reference them without requiring sudo -E.
//
// tapHelperSrc, agentSrc, and firecrackerSrc are temp-file paths for the
// extracted binaries.  installBinDir is the user-local bin directory (e.g.
// /home/user/.local/bin).
func buildSetupScript(tapHelperSrc, agentSrc, firecrackerSrc, installBinDir string) string {
	header := fmt.Sprintf(
		"export NEXUS_SETUP_TAP_HELPER_SRC=%s\nexport NEXUS_SETUP_AGENT_SRC=%s\nexport NEXUS_SETUP_FIRECRACKER_SRC=%s\nexport NEXUS_INSTALL_BIN_DIR=%s\n\n",
		shellQuote(tapHelperSrc), shellQuote(agentSrc), shellQuote(firecrackerSrc), shellQuote(installBinDir),
	)
	return header + string(firecrackerSetupScript)
}


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
// privilege mode.  For privilegeModeManual it returns errNeedsManual without
// running anything.
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

// runSetupFirecracker executes the one-time Firecracker host setup.
//
// It writes progress/manual-command output to w.  It returns a non-nil error
// if any step fails, or if manual steps are needed (non-interactive without
// passwordless sudo).
func runSetupFirecracker(w io.Writer) error {
	forceRefresh := strings.TrimSpace(os.Getenv("NEXUS_SETUP_FIRECRACKER_FORCE")) == "1"

	fmt.Fprintln(w, "==> Verifying setup...")
	if err := setupVerifyFn(); err == nil {
		if !forceRefresh {
			fmt.Fprintln(w, "==> Firecracker host setup already configured; skipping setup steps.")
			fmt.Fprintln(w, "==> Firecracker host setup complete.")
			return nil
		}
		fmt.Fprintln(w, "==> Setup already configured; force-refreshing Firecracker VM assets.")
	} else if errors.Is(err, errKVMGroupRefreshNeeded) && os.Getenv(setupKVMGroupReexecEnv) != "1" {
		cmdPath := setupCommandPath()
		fmt.Fprintln(w, "==> Setup already configured; refreshing kvm group in current session...")
		if rgErr := setupKVMGroupReexecFn(cmdPath); rgErr == nil {
			return nil
		}
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "To refresh /dev/kvm access without logging out, run:")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "  newgrp kvm")
		fmt.Fprintln(w, "  rerun your previous nexus command")
		fmt.Fprintln(w, "")
		return fmt.Errorf("setup is configured but /dev/kvm group refresh is required: %w", err)
	}

	mode := resolvePrivilegeMode()
	if mode == privilegeModeManual {
		cmdPath := setupCommandPath()
		fmt.Fprintln(w, "==> Requesting sudo to complete setup...")
		if err := setupSudoReexecFn(cmdPath); err == nil {
			fmt.Fprintln(w, "==> Verifying setup...")
			if err := setupVerifyFn(); err != nil {
				if errors.Is(err, errKVMGroupRefreshNeeded) && os.Getenv(setupKVMGroupReexecEnv) != "1" {
					fmt.Fprintln(w, "==> Refreshing kvm group in current session...")
					if rgErr := setupKVMGroupReexecFn(cmdPath); rgErr == nil {
						return nil
					}
					fmt.Fprintln(w, "")
					fmt.Fprintln(w, "To refresh /dev/kvm access without logging out, run:")
					fmt.Fprintln(w, "")
					fmt.Fprintln(w, "  newgrp kvm")
					fmt.Fprintln(w, "  rerun your previous nexus command")
					fmt.Fprintln(w, "")
				}
				return fmt.Errorf("setup verification failed after sudo setup: %w", err)
			}
			fmt.Fprintln(w, "==> Firecracker host setup complete.")
			return nil
		}

		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Run the following command to prepare firecracker prerequisites:")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "  sudo -E nexus init --project-root <absolute-repo-path>")
		fmt.Fprintln(w, "")
		return fmt.Errorf("manual privileged step required — run the sudo nexus init command above")
	}

	// ---------- step 1: resolve user-local bin directory ----------
	installBinDir, err := resolveInstallBinDir()
	if err != nil {
		return fmt.Errorf("resolve install bin dir: %w", err)
	}
	fmt.Fprintf(w, "==> Install directory: %s\n", installBinDir)

	// ---------- step 2: extract nexus-tap-helper ----------
	fmt.Fprintln(w, "==> Extracting nexus-tap-helper...")
	tapHelperPath, err := setupBuildTapHelperFn()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "    extracted: %s\n", tapHelperPath)

	// ---------- step 3: extract nexus-firecracker-agent ----------
	fmt.Fprintln(w, "==> Extracting nexus-firecracker-agent...")
	agentPath, err := setupExtractAgentFn()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "    extracted: %s\n", agentPath)

	// ---------- step 4: extract firecracker binary ----------
	fmt.Fprintln(w, "==> Extracting firecracker binary...")
	firecrackerPath, err := setupExtractFirecrackerFn()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "    extracted: %s\n", firecrackerPath)

	// ---------- step 5: build and run setup script ----------
	script := buildSetupScript(tapHelperPath, agentPath, firecrackerPath, installBinDir)

	// ---------- step 5: run (or print) the script ----------
	fmt.Fprintln(w, "==> Running Firecracker host setup script...")
	if err := setupRunScriptFn(mode, script); err != nil {
		if errors.Is(err, errNeedsManual) {
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "Run the following command to prepare firecracker prerequisites:")
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "  sudo -E nexus init --project-root <absolute-repo-path>")
			fmt.Fprintln(w, "")
			return fmt.Errorf("manual privileged step required — run the sudo nexus init command above")
		}
		return fmt.Errorf("setup script failed: %w", err)
	}

	// ---------- step 6: verify ----------
	fmt.Fprintln(w, "==> Verifying setup...")
	if err := setupVerifyFn(); err != nil {
		if errors.Is(err, errKVMGroupRefreshNeeded) && os.Getenv(setupKVMGroupReexecEnv) != "1" {
			cmdPath := setupCommandPath()
			fmt.Fprintln(w, "==> Refreshing kvm group in current session...")
			if rgErr := setupKVMGroupReexecFn(cmdPath); rgErr == nil {
				return nil
			}
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "To refresh /dev/kvm access without logging out, run:")
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "  newgrp kvm")
			fmt.Fprintln(w, "  rerun your previous nexus command")
			fmt.Fprintln(w, "")
		}
		return fmt.Errorf("setup verification failed: %w", err)
	}

	fmt.Fprintln(w, "==> Firecracker host setup complete.")
	return nil
}

// resolveInstallBinDir returns the user-local bin directory, creating it if needed.
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

// verifyFirecrackerSetup checks that the setup succeeded.
func verifyFirecrackerSetup() error {
	installBinDir, err := resolveInstallBinDir()
	if err != nil {
		return fmt.Errorf("resolve install bin dir: %w", err)
	}

	if _, err := os.Stat(filepath.Join(installBinDir, "firecracker")); err != nil {
		return fmt.Errorf("firecracker not found at %s: %w", installBinDir, err)
	}

	tapHelperPath := filepath.Join(installBinDir, "nexus-tap-helper")
	if _, err := os.Stat(tapHelperPath); err != nil {
		return fmt.Errorf("nexus-tap-helper not found at %s: %w", tapHelperPath, err)
	}
	out, err := exec.Command("getcap", tapHelperPath).Output()
	if err != nil {
		return fmt.Errorf("getcap failed: %w", err)
	}
	if !strings.Contains(string(out), "cap_net_admin") {
		return fmt.Errorf("nexus-tap-helper at %s lacks cap_net_admin", tapHelperPath)
	}
	ipOut, err := exec.Command("ip", "link", "show", "nexusbr0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bridge nexusbr0 not found: %w", err)
	}
	if !strings.Contains(string(ipOut), "UP") {
		return fmt.Errorf("bridge nexusbr0 exists but is not UP")
	}
	routeOut, err := exec.Command("ip", "route", "show", "dev", "nexusbr0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("unable to inspect nexusbr0 route: %w", err)
	}
	if strings.Contains(string(routeOut), "linkdown") {
		// linkdown is expected before any TAP device is attached; setup should
		// still be treated as successful if bridge and assets are in place.
	}

	// Verify VM assets
	if _, err := os.Stat(DefaultVMKernelPath); err != nil {
		return fmt.Errorf("VM kernel not found at %s: %w", DefaultVMKernelPath, err)
	}
	kernelFD, err := os.Open(DefaultVMKernelPath)
	if err != nil {
		return fmt.Errorf("VM kernel not readable at %s: %w", DefaultVMKernelPath, err)
	}
	_ = kernelFD.Close()
	if _, err := os.Stat(DefaultVMRootfsPath); err != nil {
		return fmt.Errorf("VM rootfs not found at %s: %w", DefaultVMRootfsPath, err)
	}
	rootfsFD, err := os.OpenFile(DefaultVMRootfsPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("VM rootfs not read/write accessible at %s: %w", DefaultVMRootfsPath, err)
	}
	_ = rootfsFD.Close()

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
