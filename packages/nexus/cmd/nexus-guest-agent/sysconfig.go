//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// mountKernelFilesystemsFunc is overridable in tests.
var mountKernelFilesystemsFunc = mountKernelFilesystems

// setupDNSFunc is overridable in tests.
var setupDNSFunc = setupDNS

func bootstrapGuestEnvironment(pid int) {
	ensureAgentProcessPath()

	primary := isPrimaryAgent()
	if primary {
		mountKernelFilesystemsFunc()
		if err := setupPTYDevices(); err != nil {
			emitDiagnostic("agent pty device setup failed (non-fatal): %v", err)
		}
		emitDiagnostic("agent primary kernel filesystems mounted (pid=%d container_mode=%v)", pid, os.Getenv("NEXUS_CONTAINER_MODE") == "1")
	}

	if err := setupWorkspaceMountFunc(); err != nil {
		emitDiagnostic("agent workspace mount failed (non-fatal): %v", err)
	} else {
		emitDiagnostic("agent workspace mounted")
	}

	// Mount the host config drive and apply configs.
	if err := applyHostConfigDrive(); err != nil {
		emitDiagnostic("agent host config drive (non-fatal): %v", err)
	} else {
		emitDiagnostic("agent host config applied")
	}

	if err := setupNetwork(); err != nil {
		emitDiagnostic("agent network setup failed (non-fatal): %v", err)
	} else {
		emitDiagnostic("agent network configured")
	}

	if primary {
		maybeStartSSHDInGuest()
	}

	setupDNSFunc()
	emitDiagnostic("agent dns configured")
}

// runStreamed runs a command with output wired directly to /dev/console so
// every byte appears in the hvc0 serial log without Go-level buffering.
// /dev/kmsg rate-limits burst writes, so we bypass emitDiagnostic here and
// write the header/trailer via emitDiagnostic only (two lines total).
func runStreamed(ctx context.Context, env []string, prefix, name string, args ...string) error {
	emitDiagnostic("agent run: starting %s", prefix)
	start := time.Now()

	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}

	// Attach stdout/stderr directly to the serial console so output is
	// visible in real time without buffering.
	console, consErr := os.OpenFile("/dev/console", os.O_WRONLY|os.O_APPEND, 0)
	if consErr == nil {
		cmd.Stdout = console
		cmd.Stderr = console
	}

	if err := cmd.Start(); err != nil {
		if console != nil {
			_ = console.Close()
		}
		emitDiagnostic("agent run: %s failed to start (%v)", prefix, time.Since(start).Round(time.Second))
		return fmt.Errorf("start %s: %w", name, err)
	}

	err := cmd.Wait()
	elapsed := time.Since(start).Round(time.Second)
	if console != nil {
		_ = console.Close()
	}
	if err != nil {
		emitDiagnostic("agent run: %s exited with error after %v", prefix, elapsed)
		return fmt.Errorf("%s exited: %w", name, err)
	}
	emitDiagnostic("agent run: %s completed in %v", prefix, elapsed)
	return nil
}

// ensureGuestBasePackages installs the developer toolchain (make, nodejs, npm,
// docker.io, build-essential) via apt-get inside the VM.  The VM is a real root
// environment so apt-get works without any user-namespace restrictions.
//
// A versioned stamp file in the rootfs prevents
// re-running on every workspace start — packages only install once per rootfs.
func ensureGuestBasePackages() error {
	start := time.Now()
	const stampFile = "/var/lib/nexus-tools-base-v7"
	if _, err := os.Stat(stampFile); err == nil {
		emitDiagnostic("agent base packages: already installed (stamp found)")
		return nil
	}

	// Backward-compat: older images may have v6, v5 or monolithic v4 stamps.
	// Promote only if the full required toolchain is actually present.
	if _, err := os.Stat("/var/lib/nexus-tools-base-v6"); err == nil {
		if toolchainPresentInPATH(os.Environ()) {
			_ = os.MkdirAll("/var/lib", 0o755)
			_ = os.WriteFile(stampFile, []byte("ok\n"), 0o644)
			emitDiagnostic("agent base packages: promoted legacy v6 stamp to v7")
			return nil
		}
		emitDiagnostic("agent base packages: legacy v6 stamp found but toolchain incomplete; reinstalling")
	}
	if _, err := os.Stat("/var/lib/nexus-tools-base-v5"); err == nil {
		if toolchainPresentInPATH(os.Environ()) {
			_ = os.MkdirAll("/var/lib", 0o755)
			_ = os.WriteFile(stampFile, []byte("ok\n"), 0o644)
			emitDiagnostic("agent base packages: promoted legacy v5 stamp to v7")
			return nil
		}
		emitDiagnostic("agent base packages: legacy v5 stamp found but toolchain incomplete; reinstalling")
	}
	if _, err := os.Stat("/var/lib/nexus-tools-installed-v4"); err == nil {
		if toolchainPresentInPATH(os.Environ()) {
			_ = os.MkdirAll("/var/lib", 0o755)
			_ = os.WriteFile(stampFile, []byte("ok\n"), 0o644)
			emitDiagnostic("agent base packages: promoted legacy v4 stamp to v7")
			return nil
		}
		emitDiagnostic("agent base packages: legacy v4 stamp found but toolchain incomplete; reinstalling")
	}

	if _, err := exec.LookPath("apt-get"); err != nil {
		emitDiagnostic("agent base packages: apt-get not found, skipping")
		return fmt.Errorf("apt-get not found")
	}

	emitDiagnostic("agent base packages: installing full package set (docker/node/npm/tooling prereqs)...")

	pkgs := []string{
		"make",
		"git",
		"curl",
		"wget",
		"docker.io",
		"containerd",
		"build-essential",
		"nodejs",
		"npm",
		"docker-compose-v2",
		"python3",
		"python3-pip",
		// busybox-static provides udhcpc (DHCP client) used by the agent's
		// setupNetwork to acquire an IP from passt at VM boot time.
		"busybox-static",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
	)

	// Some minimal rootfs images omit /var/log/apt/ and /var/log/dpkg.log,
	// which causes apt-get to exit 100 with "E: Directory '/var/log/apt/' missing"
	// even when packages install successfully.  Create them upfront so apt is happy.
	for _, dir := range []string{"/var/log/apt", "/var/cache/apt/archives/partial"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			emitDiagnostic("agent base packages: mkdir %s failed: %v", dir, err)
		}
	}
	if _, err := os.Stat("/var/log/dpkg.log"); os.IsNotExist(err) {
		if f, err := os.OpenFile("/var/log/dpkg.log", os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
		}
	}

	emitDiagnostic("agent base packages: running apt-get update...")
	if err := runAptGetWithRetry(ctx, env, "apt-get update", "update"); err != nil {
		emitDiagnostic("agent base packages: apt-get update FAILED: %v", err)
		return fmt.Errorf("apt-get update: %w", err)
	}
	emitDiagnostic("agent base packages: apt-get update OK")

	emitDiagnostic("agent base packages: installing package set (%d packages)...", len(pkgs))
	installArgs := append([]string{"install", "-y", "--no-install-recommends"}, pkgs...)
	if err := runAptGetWithRetry(ctx, env, "apt-get install base-package-set", installArgs...); err != nil {
		emitDiagnostic("agent base packages: install package set FAILED: %v", err)
		return fmt.Errorf("apt-get install package set: %w", err)
	}
	emitDiagnostic("agent base packages: install package set OK")

	if err := ensureGuestCLITools(); err != nil {
		return fmt.Errorf("install cli tools: %w", err)
	}
	if !toolchainPresentInPATH(os.Environ()) {
		return fmt.Errorf("toolchain verification failed: required binaries are still missing")
	}

	// Write stamp so subsequent boots skip this step.
	_ = os.MkdirAll("/var/lib", 0o755)
	_ = os.WriteFile(stampFile, []byte("ok\n"), 0o644)
	// Legacy stamp cleanup (best-effort).
	_ = os.Remove("/var/lib/nexus-tools-base-v6")
	_ = os.Remove("/var/lib/nexus-tools-base-v5")
	_ = os.Remove("/var/lib/nexus-tools-optional-v5")
	emitDiagnostic("agent base packages: installed successfully (total %v)", time.Since(start).Round(time.Second))
	return nil
}

func runAptGetWithRetry(ctx context.Context, env []string, label string, args ...string) error {
	const maxAttempts = 4

	aptArgs := []string{
		"-o", "Acquire::Retries=5",
		"-o", "Acquire::http::Timeout=20",
		"-o", "Acquire::https::Timeout=20",
	}
	if len(args) > 0 && args[0] == "update" {
		// apt-get update can otherwise return zero even when some index fetches
		// fail. Force non-zero so retry logic handles transient DNS outages.
		aptArgs = append(aptArgs, "-o", "APT::Update::Error-Mode=any")
	}
	aptArgs = append(aptArgs, args...)

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			emitDiagnostic("agent base packages: retrying %s (%d/%d)", label, attempt, maxAttempts)
		}
		if err := runStreamed(ctx, env, label, "apt-get", aptArgs...); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if ctx.Err() != nil {
			break
		}
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
	}
	return lastErr
}

func ensureGuestCLITools() error {
	start := time.Now()
	type cliSpec struct {
		binary string
		pkg    string
	}

	tools := []cliSpec{
		{binary: "opencode", pkg: "opencode-ai@latest"},
		{binary: "codex", pkg: "@openai/codex@latest"},
		{binary: "claude", pkg: "@anthropic-ai/claude-code@latest"},
	}

	npmPath, err := lookPathInEnv("npm", os.Environ())
	if err != nil || strings.TrimSpace(npmPath) == "" {
		return fmt.Errorf("npm not found: %w", err)
	}

	env := ensurePathInEnv(os.Environ())

	missing := make([]cliSpec, 0, len(tools))
	for _, tool := range tools {
		if _, err := lookPathInEnv(tool.binary, env); err == nil {
			emitDiagnostic("agent cli tools: %s already installed", tool.binary)
			continue
		}
		missing = append(missing, tool)
	}
	if len(missing) == 0 {
		emitDiagnostic("agent cli tools: all tools present")
		return nil
	}

	var packages []string
	for _, tool := range missing {
		packages = append(packages, tool.pkg)
		emitDiagnostic("agent cli tools: will install %s (%s)", tool.binary, tool.pkg)
	}

	// Nuke the entire global node_modules tree and recreate it.  Previous
	// partial installs leave behind temp directories (.package-*) that cause
	// npm's atomic rename to fail with ENOTEMPTY.  rm -rf is the only reliable
	// fix inside the VM.
	npmGlobalDir := "/usr/local/lib/node_modules"
	_ = os.RemoveAll(npmGlobalDir)
	_ = os.MkdirAll(npmGlobalDir, 0o755)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	args := append([]string{"install", "-g", "--force"}, packages...)
	emitDiagnostic("agent cli tools: running npm install -g %s...", strings.Join(packages, " "))
	if err := runNPMWithRetry(ctx, env, npmPath, args...); err != nil {
		return fmt.Errorf("npm install global tools: %w", err)
	}

	// Verify each binary is now resolvable.
	envAfter := ensurePathInEnv(os.Environ())
	for _, tool := range missing {
		if _, err := lookPathInEnv(tool.binary, envAfter); err != nil {
			return fmt.Errorf("%s not found after install: %w", tool.binary, err)
		} else {
			emitDiagnostic("agent cli tools: %s installed OK", tool.binary)
		}
	}
	emitDiagnostic("agent cli tools: installed successfully (total %v)", time.Since(start).Round(time.Second))
	return nil
}

func runNPMWithRetry(ctx context.Context, env []string, npmPath string, args ...string) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			emitDiagnostic("agent cli tools: retrying npm install (%d/%d)", attempt, maxAttempts)
		}
		if err := runStreamed(ctx, env, "npm install", npmPath, args...); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if ctx.Err() != nil {
			break
		}
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}
	}
	return lastErr
}

func toolchainPresentInPATH(env []string) bool {
	required := []string{"docker", "dockerd", "node", "npm", "opencode", "codex", "claude"}
	for _, bin := range required {
		if _, err := lookPathInEnv(bin, env); err != nil {
			return false
		}
	}
	return true
}

func ensureAgentProcessPath() {
	if strings.TrimSpace(os.Getenv("PATH")) == "" {
		_ = os.Setenv("PATH", defaultAgentPath)
	}
}

func ensureDockerDaemon() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	if _, err := exec.LookPath("dockerd"); err != nil {
		return nil
	}
	if err := exec.Command("docker", "info").Run(); err == nil {
		emitDiagnostic("agent docker daemon already available")
		return nil
	}

	// In virtiofs mode: Docker data lives on /dev/vdc (mounted at /var/lib/docker),
	// a dedicated sparse ext4 so overlay2 runs on a native kernel filesystem.
	// In legacy mode: data lives inside /workspace (native ext4 block device).
	dataRoot := "/workspace/.nexus-docker"
	execRoot := "/workspace/.nexus-docker-exec"
	socketPath := "/var/run/docker.sock"
	if isVirtiofsWorkspaceMode() {
		dataRoot = "/var/lib/docker"
		execRoot = "/var/lib/docker/exec"
		// /run lives on virtiofs root and dockerd cannot chown a socket there.
		// Keep the real socket on docker-data ext4 and expose /var/run/docker.sock
		// as a symlink for default CLI behavior.
		socketPath = "/var/lib/docker/docker.sock"
	}
	dockerHost := "unix://" + socketPath

	_ = os.Remove("/var/run/docker.pid")
	_ = os.Remove("/var/run/docker.sock")
	_ = os.Remove(socketPath)
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir docker data root: %w", err)
	}
	if err := os.MkdirAll(execRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir docker exec root: %w", err)
	}
	if isVirtiofsWorkspaceMode() {
		// In hybrid mode /run is a writable tmpfs (block rootfs), so containerd
		// can use /run/containerd directly. Remove any stale symlink left from a
		// previous virtiofs-only boot to avoid "mkdir /run/containerd: file exists".
		if info, err := os.Lstat("/run/containerd"); err == nil && info.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove("/run/containerd")
		}
		if err := ensureRunPathRedirect("/run/containerd", filepath.Join(execRoot, "run-containerd")); err != nil {
			emitDiagnostic("agent docker daemon: /run/containerd redirect failed (non-fatal): %v", err)
		}
	}

	// Probe NAT table support before enabling Docker's iptables integration.
	// Some libkrun guest kernels do not expose the required netfilter modules;
	// in that case dockerd must run without bridge/NAT management.
	iptablesFlag := "--iptables=false"
	if legacyPath, err := exec.LookPath("iptables-legacy"); err == nil {
		probe := exec.Command(legacyPath, "-w", "-t", "nat", "-L", "-n")
		if err := probe.Run(); err == nil {
			_ = exec.Command("update-alternatives", "--set", "iptables", legacyPath).Run()
			if legacy6Path, err := exec.LookPath("ip6tables-legacy"); err == nil {
				_ = exec.Command("update-alternatives", "--set", "ip6tables", legacy6Path).Run()
			}
			iptablesFlag = "--iptables=true"
		} else {
			emitDiagnostic("agent docker daemon: iptables nat unavailable; disabling Docker iptables integration")
		}
	}

	logFile, err := os.OpenFile("/tmp/nexus-agent-dockerd.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open dockerd log: %w", err)
	}
	defer logFile.Close()

	args := []string{
		"--host=" + dockerHost,
		"--data-root=" + dataRoot,
		"--exec-root=" + execRoot,
		"--storage-driver=overlay2",
		iptablesFlag,
	}
	if iptablesFlag == "--iptables=false" {
		// Keep bridge disabled (no iptables NAT management) but allow IP
		// forwarding — we pre-enable it in the kernel during network setup.
		args = append(args, "--bridge=none")
	}
	cmd := exec.Command("dockerd", args...)
	cmd.Env = ensurePathInEnv(os.Environ())
	disableRaw := strings.TrimSpace(os.Getenv("NEXUS_DOCKER_DISABLE_IPTABLES_RAW"))
	if disableRaw == "" || disableRaw == "1" {
		cmd.Env = append(cmd.Env, "DOCKER_INSECURE_NO_IPTABLES_RAW=1")
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dockerd: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		check := exec.Command("docker", "info")
		check.Env = append(ensurePathInEnv(os.Environ()), "DOCKER_HOST="+dockerHost)
		if err := check.Run(); err == nil {
			if isVirtiofsWorkspaceMode() {
				_ = os.MkdirAll("/var/run", 0o755)
				_ = os.Remove("/var/run/docker.sock")
				_ = os.Symlink(socketPath, "/var/run/docker.sock")
			}
			emitDiagnostic("agent docker daemon ready")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("dockerd did not become ready within 15s (see /tmp/nexus-agent-dockerd.log)")
}

func ensureRunPathRedirect(runPath, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("mkdir target %s: %w", targetDir, err)
	}

	info, err := os.Lstat(runPath)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			dest, readErr := os.Readlink(runPath)
			if readErr == nil && dest == targetDir {
				return nil
			}
		}
		if rmErr := os.RemoveAll(runPath); rmErr != nil {
			return fmt.Errorf("remove existing %s: %w", runPath, rmErr)
		}
	case errors.Is(err, os.ErrNotExist):
		// no-op
	default:
		return fmt.Errorf("stat %s: %w", runPath, err)
	}

	if err := os.Symlink(targetDir, runPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("symlink %s -> %s: %w", runPath, targetDir, err)
	}
	return nil
}

// setupNetworkFunc is the function used to configure the guest network interface.
// Overridable in tests.
var setupNetworkFunc = realSetupNetwork

// setupNetwork brings up eth0 via DHCP (udhcpc or dhcpcd) or static fallback.
// It is called at PID1 boot before setupDNS so that DNS resolution works.
func setupNetwork() error {
	return setupNetworkFunc()
}

// maybeStartSSHDInGuest launches OpenSSH sshd when the rootfs provides it.
// The rootfs is built by nexus daemon bootstrap which installs openssh-server,
// so sshd should always be present. Host keys are generated on first boot.
// This enables VS Code / Cursor Remote-SSH to attach directly to the micro-VM.
func maybeStartSSHDInGuest() {
	const sshdPath = "/usr/sbin/sshd"
	if _, err := os.Stat(sshdPath); err != nil {
		emitDiagnostic("sshd: %s not found in rootfs — rebuild rootfs with 'nexus daemon start' to install openssh-server", sshdPath)
		return
	}

	// Generate any missing host keys so sshd starts cleanly.
	for _, keyType := range []string{"rsa", "ecdsa", "ed25519"} {
		keyPath := "/etc/ssh/ssh_host_" + keyType + "_key"
		if _, err := os.Stat(keyPath); err != nil {
			out, kErr := exec.Command("ssh-keygen", "-q", "-N", "", "-t", keyType, "-f", keyPath).CombinedOutput()
			if kErr != nil {
				emitDiagnostic("sshd: generate %s host key failed (non-fatal): %v: %s", keyType, kErr, strings.TrimSpace(string(out)))
			} else {
				emitDiagnostic("sshd: generated %s host key", keyType)
			}
		}
	}

	// /run/sshd must be a directory (privilege separation chroot) — not a file.
	// Remove any stale file that may exist (e.g. from a previous bad attempt)
	// and re-create as a directory.
	const sshdRunDir = "/run/sshd"
	if info, err := os.Lstat(sshdRunDir); err == nil && !info.IsDir() {
		_ = os.Remove(sshdRunDir)
	}
	_ = os.MkdirAll(sshdRunDir, 0o755)

	// Ensure /var/empty exists for sshd privilege separation.
	_ = os.MkdirAll("/var/empty", 0o755)

	// libkrun's guest kernel seccomp sandbox is incompatible with sshd's default
	// "sandbox" privilege-separation mode (pre-auth child crashes with
	// "exited with irqs disabled").  Use the legacy no-separation mode — the
	// micro-VM is single-user and isolated anyway.
	_ = exec.Command("bash", "-c", "echo 'UsePrivilegeSeparation no' >> /etc/ssh/sshd_config").Run()
	_ = exec.Command("bash", "-c", "echo 'PermitRootLogin prohibit-password' >> /etc/ssh/sshd_config").Run()

	// Run sshd in foreground+stderr mode so its output appears in the hvc0
	// serial log. sshd's -D keeps it in the foreground, -e logs to stderr.
	// We open /dev/console directly so the output appears in the guest serial
	// log regardless of what fd 2 (stderr) is connected to in the agent.
	consoleF, _ := os.OpenFile("/dev/console", os.O_WRONLY|os.O_APPEND, 0)
	if consoleF == nil {
		consoleF = os.Stderr
	}
	cmd := exec.Command(sshdPath, "-D", "-e")
	cmd.Stdout = consoleF
	cmd.Stderr = consoleF
	if err := cmd.Start(); err != nil {
		_ = consoleF.Close()
		emitDiagnostic("sshd: start failed (non-fatal): %v", err)
		return
	}
	_ = consoleF.Close() // child inherited the fd; parent can close its copy
	emitDiagnostic("sshd: started (pid=%d) foreground+stderr mode", cmd.Process.Pid)
	// Reap sshd if it exits unexpectedly so it doesn't become a zombie.
	go func() {
		if err := cmd.Wait(); err != nil {
			emitDiagnostic("sshd: exited unexpectedly: %v", err)
		} else {
			emitDiagnostic("sshd: exited cleanly")
		}
	}()
}

func realSetupNetwork() error {
	// Enable IPv4 forwarding so Docker containers can reach external networks
	// even when Docker's iptables integration is disabled.
	_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()

	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set lo up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if !interfaceExists("eth0") {
		if isTSINetworkMode() {
			emitDiagnostic("network: eth0 not present in TSI/no-NIC mode; skipping DHCP/static interface setup")
			return nil
		}
		return fmt.Errorf("network interface eth0 not found")
	}

	// Bring the interface up first so DHCP can configure it.
	if out, err := exec.Command("ip", "link", "set", "eth0", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set eth0 up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if _, err := exec.LookPath("udhcpc"); err == nil {
		// Run udhcpc in one-shot mode: -n (exit on failure), -q (quit after obtaining
		// lease), -t 10 (retry 10 times), -T 3 (3s between retries) -> max ~30s wait.
		out, err := exec.Command(
			"udhcpc", "-i", "eth0", "-n", "-q", "-t", "10", "-T", "3",
		).CombinedOutput()
		if err == nil {
			if hasUsableIPv4OnEth0() {
				return nil
			}
			emitDiagnostic("udhcpc exited successfully but eth0 has no usable IPv4; falling through to other DHCP/static methods")
		}
		emitDiagnostic("udhcpc failed: %v: %s", err, strings.TrimSpace(string(out)))
	} else if _, err := exec.LookPath("dhcpcd"); err == nil {
		// dhcpcd is available (installed on Ubuntu when busybox is not).
		// -1 (--oneshot): exit after the first interface is configured.
		// This makes dhcpcd behave like a one-shot DHCP client.
		out, err := exec.Command("dhcpcd", "-1", "eth0").CombinedOutput()
		if err == nil {
			if hasUsableIPv4OnEth0() {
				return nil
			}
			emitDiagnostic("dhcpcd exited successfully but eth0 has no usable IPv4; falling through to static fallback")
		}
		emitDiagnostic("dhcpcd failed: %v: %s", err, strings.TrimSpace(string(out)))
	} else if _, err := exec.LookPath("dhclient"); err == nil {
		out, err := exec.Command("dhclient", "-1", "-v", "eth0").CombinedOutput()
		if err == nil {
			if hasUsableIPv4OnEth0() {
				return nil
			}
			emitDiagnostic("dhclient exited successfully but eth0 has no usable IPv4; falling through to static fallback")
		}
		emitDiagnostic("dhclient failed: %v: %s", err, strings.TrimSpace(string(out)))
	}

	emitDiagnostic("all DHCP clients failed or unavailable, falling back to static guest IP")
	if err := configureStaticGuestNetwork(); err != nil {
		return fmt.Errorf("static network fallback failed: %w", err)
	}
	return nil
}

func interfaceExists(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join("/sys/class/net", name)); err == nil {
		return true
	}
	return false
}

// hasUsableIPv4OnEth0 reports whether eth0 has a global IPv4 address and a
// default route. Some DHCP clients can exit 0 after configuring only IPv6,
// which leaves host->guest TCP forwarding broken for passt/libkrun SSH.
func hasUsableIPv4OnEth0() bool {
	addrOut, addrErr := exec.Command("ip", "-4", "addr", "show", "dev", "eth0", "scope", "global").CombinedOutput()
	routeOut, routeErr := exec.Command("ip", "route", "show", "default").CombinedOutput()
	if addrErr == nil && routeErr == nil && networkLooksUsable(string(addrOut), string(routeOut)) {
		return true
	}
	emitDiagnostic(
		"network verify: unusable IPv4/default route after DHCP (addrErr=%v routeErr=%v addr=%q route=%q)",
		addrErr,
		routeErr,
		strings.TrimSpace(string(addrOut)),
		strings.TrimSpace(string(routeOut)),
	)
	return false
}

func networkLooksUsable(addrOutput, routeOutput string) bool {
	hasUsableAddr := false
	for _, line := range strings.Split(addrOutput, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "inet ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ipStr, _, _ := strings.Cut(fields[1], "/")
		ip := net.ParseIP(strings.TrimSpace(ipStr))
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		hasUsableAddr = true
		break
	}
	if !hasUsableAddr {
		return false
	}

	for _, line := range strings.Split(routeOutput, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "default via ") && strings.Contains(line, " dev eth0") {
			return true
		}
	}
	return false
}

// readNexusGateway returns the guest's default gateway IP. It checks, in order:
//  1. The host config drive (.nexus-gateway) — set for normal workspace VMs.
//  2. The kernel cmdline nexus.gw= parameter — set for bake VMs.
//  3. A compiled-in default (192.168.0.1) which matches the host's passt fallback.
func readNexusGateway() string {
	const configDriveMount = "/run/nexus-host"
	const defaultGW = "192.168.0.1"

	// 1. Host config drive (workspace VMs).
	if data, err := os.ReadFile(filepath.Join(configDriveMount, ".nexus-gateway")); err == nil {
		if gw := strings.TrimSpace(string(data)); gw != "" {
			return gw
		}
	}

	// 2. Kernel cmdline nexus.gw= (bake VMs and any VM without a config drive).
	if cmdline, err := os.ReadFile("/proc/cmdline"); err == nil {
		for _, field := range strings.Fields(string(cmdline)) {
			if strings.HasPrefix(field, "nexus.gw=") {
				if gw := strings.TrimPrefix(field, "nexus.gw="); gw != "" {
					return gw
				}
			}
		}
	}

	return defaultGW
}

func configureStaticGuestNetwork() error {
	macBytes, err := os.ReadFile("/sys/class/net/eth0/address")
	if err != nil {
		return fmt.Errorf("read eth0 mac address: %w", err)
	}

	gateway := readNexusGateway()
	ip, err := staticGuestIPForMAC(strings.TrimSpace(string(macBytes)), gateway)
	if err != nil {
		return err
	}

	if out, err := exec.Command("ip", "addr", "replace", ip+"/16", "dev", "eth0").CombinedOutput(); err != nil {
		return fmt.Errorf("ip addr replace %s/16 dev eth0: %w: %s", ip, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("ip", "route", "replace", "default", "via", gateway, "dev", "eth0").CombinedOutput(); err != nil {
		return fmt.Errorf("ip route replace default via %s dev eth0: %w: %s", gateway, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// staticGuestIPForMAC derives a guest IP in the same /16 subnet as the gateway,
// using the last two MAC octets as the host portion.
// For gateway "172.20.0.1" and MAC "aa:bb:cc:dd:12:34" → "172.20.18.52".
func staticGuestIPForMAC(mac, gateway string) (string, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(mac)), ":")
	if len(parts) != 6 {
		return "", fmt.Errorf("invalid mac address format: %q", mac)
	}

	b4, err := strconv.ParseUint(parts[4], 16, 8)
	if err != nil {
		return "", fmt.Errorf("parse mac byte 5: %w", err)
	}
	b5, err := strconv.ParseUint(parts[5], 16, 8)
	if err != nil {
		return "", fmt.Errorf("parse mac byte 6: %w", err)
	}

	// Derive subnet prefix from gateway (first two octets of "a.b.c.d").
	gwParts := strings.SplitN(gateway, ".", 4)
	if len(gwParts) < 2 {
		return fmt.Sprintf("172.26.%d.%d", b4, b5), nil
	}
	return fmt.Sprintf("%s.%s.%d.%d", gwParts[0], gwParts[1], b4, b5), nil
}

// setupDNSPath is the path to /etc/resolv.conf. Overridable in tests.
var setupDNSPath = "/etc/resolv.conf"

// setupDNS writes /etc/resolv.conf with public DNS servers if it is empty or
// missing. This is needed because the kernel ip= cmdline arg configures the
// network interface but does not create /etc/resolv.conf.
//
// In libkrun TSI mode (no eth0) DNS UDP packets have no interface to route
// through, but TSI does intercept TCP sockets. "options use-vc" forces
// glibc's resolver to use TCP for all DNS queries so they transit TSI.
func setupDNS() {
	tsiMode := isTSINetworkMode()
	kernelDNS := readNexusDNSServers()
	useKernelDNS := len(kernelDNS) > 0

	dnsServers := kernelDNS
	if len(dnsServers) == 0 {
		dnsServers = []string{"8.8.8.8", "1.1.1.1"}
	}

	var b strings.Builder
	for _, server := range dnsServers {
		b.WriteString("nameserver ")
		b.WriteString(server)
		b.WriteString("\n")
	}
	if tsiMode {
		// TSI: force DNS over TCP (use-vc) so queries go through TSI, not UDP.
		b.WriteString("options use-vc\n")
	}
	content := b.String()

	// Prefer host-provided resolver config when present and valid (real file,
	// not a broken symlink). Always ensure use-vc is set in TSI mode.
	if fi, statErr := os.Lstat(setupDNSPath); statErr == nil && fi.Mode()&os.ModeSymlink == 0 {
		if data, err := os.ReadFile(setupDNSPath); err == nil && dnsConfigLooksUsable(string(data)) {
			if !tsiMode && !useKernelDNS {
				return
			}
			// In TSI mode: patch existing config to add use-vc if not present.
			// When kernel DNS is provided (bake mode), force writing the provided
			// nameservers to avoid relying on stale host image defaults.
			if useKernelDNS {
				_ = os.WriteFile(setupDNSPath, []byte(content), 0o644)
				return
			}
			existing := string(data)
			if !strings.Contains(existing, "use-vc") {
				existing = strings.TrimRight(existing, "\n") + "\noptions use-vc\n"
				_ = os.WriteFile(setupDNSPath, []byte(existing), 0o644)
			}
			return
		}
	}

	_ = os.MkdirAll("/etc", 0o755)
	// Remove any broken symlink first (Ubuntu minimal cloud image ships
	// /etc/resolv.conf → ../run/systemd/resolve/stub-resolv.conf which
	// points to a path that doesn't exist inside the VM). os.WriteFile
	// follows symlinks, so it would silently fail against the missing target;
	// unlinking first ensures we create a real file at the path.
	_ = os.Remove(setupDNSPath)
	_ = os.WriteFile(setupDNSPath, []byte(content), 0o644)
}

func readNexusDNSServers() []string {
	cmdline, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return nil
	}
	return parseNexusDNSServersFromCmdline(string(cmdline))
}

func parseNexusDNSServersFromCmdline(cmdline string) []string {
	for _, field := range strings.Fields(cmdline) {
		if !strings.HasPrefix(field, "nexus.dns=") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(field, "nexus.dns="))
		if raw == "" {
			return nil
		}
		seen := map[string]struct{}{}
		servers := make([]string, 0, 2)
		for _, part := range strings.Split(raw, ",") {
			ip := net.ParseIP(strings.TrimSpace(part))
			if ip == nil || ip.IsLoopback() {
				continue
			}
			key := ip.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			servers = append(servers, key)
			if len(servers) >= 3 {
				break
			}
		}
		return servers
	}
	return nil
}

func isTSINetworkMode() bool {
	if isVirtiofsWorkspaceMode() {
		return true
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == "nexus.tsi=1" {
			return true
		}
	}
	return false
}

func dnsConfigLooksUsable(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "nameserver" {
			// Loopback resolvers (127.x.x.x) are host-only stubs like
			// systemd-resolved that don't exist inside the VM.
			if !strings.HasPrefix(fields[1], "127.") {
				return true
			}
		}
	}
	return false
}
