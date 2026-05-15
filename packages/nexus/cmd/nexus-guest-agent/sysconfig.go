//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
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
	const stampFile = "/var/lib/nexus-tools-base-v15"
	if _, err := os.Stat(stampFile); err == nil {
		emitDiagnostic("agent base packages: already installed (stamp found)")
		return nil
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
		"docker-compose-v2",
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

	misePath, err := ensureGuestCLITools()
	if err != nil {
		return fmt.Errorf("install cli tools: %w", err)
	}
	if !toolchainPresentInPATH(os.Environ()) {
		return fmt.Errorf("toolchain verification failed: required binaries are still missing")
	}

	slimGuestCachesBeforeStamp(ctx, env, misePath)

	// Write stamp so subsequent boots skip this step.
	_ = os.MkdirAll("/var/lib", 0o755)
	_ = os.WriteFile(stampFile, []byte("ok\n"), 0o644)
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

// slimGuestCachesBeforeStamp drops caches and offline documentation so the
// rootfs (especially CI-baked ext4.gz) compresses smaller. Runs only once —
// immediately before the base-package stamp is written — so later apt usage
// starts with a normal `apt-get update`.
func slimGuestCachesBeforeStamp(ctx context.Context, env []string, misePath string) {
	slimCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()

	if isBakeMode() {
		slimGuestCachesBakeOnly(slimCtx, misePath)
	}

	emitDiagnostic("agent slim: pruning apt archives and package indexes")
	aptCommon := []string{"-o", "Acquire::Retries=3", "-o", "DEBIAN_FRONTEND=noninteractive"}
	runAptQuiet := func(args ...string) {
		cmd := exec.CommandContext(slimCtx, "apt-get", append(append([]string{}, aptCommon...), args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			emitDiagnostic("agent slim: apt-get %v failed (non-fatal): %v — %s", args, err, strings.TrimSpace(string(out)))
		}
	}
	runAptQuiet("clean")
	runAptQuiet("autoclean")

	if err := os.RemoveAll("/var/lib/apt/lists"); err != nil {
		emitDiagnostic("agent slim: remove apt lists dir failed (non-fatal): %v", err)
	}
	if err := os.MkdirAll("/var/lib/apt/lists/partial", 0o755); err != nil {
		emitDiagnostic("agent slim: recreate apt lists/partial failed (non-fatal): %v", err)
	}

	if err := os.RemoveAll("/var/cache/apt/archives/partial"); err != nil {
		emitDiagnostic("agent slim: clear apt archives/partial failed (non-fatal): %v", err)
	}
	_ = os.MkdirAll("/var/cache/apt/archives/partial", 0o755)
}

func slimGuestCachesBakeOnly(ctx context.Context, misePath string) {
	emitDiagnostic("agent slim (bake): clearing toolchain caches and stripping man/info pages")

	if strings.TrimSpace(misePath) != "" {
		clear := exec.CommandContext(ctx, misePath, "cache", "clear")
		clear.Env = ensurePathInEnv(os.Environ())
		if out, err := clear.CombinedOutput(); err != nil {
			emitDiagnostic("agent slim (bake): mise cache clear failed (non-fatal): %v — %s", err, strings.TrimSpace(string(out)))
		}
		npmClean := exec.CommandContext(ctx, misePath, "x", "node@20", "--", "npm", "cache", "clean", "--force")
		npmClean.Env = ensurePathInEnv(os.Environ())
		if out, err := npmClean.CombinedOutput(); err != nil {
			emitDiagnostic("agent slim (bake): npm cache clean failed (non-fatal): %v — %s", err, strings.TrimSpace(string(out)))
		}
	}

	// Strip offline documentation only (keep /usr/share/doc copyrights).
	for _, p := range []string{"/usr/share/man", "/usr/share/info"} {
		if err := os.RemoveAll(p); err != nil {
			emitDiagnostic("agent slim (bake): remove %s failed (non-fatal): %v", p, err)
		}
	}

	matches, _ := filepath.Glob("/tmp/*")
	for _, m := range matches {
		if err := os.RemoveAll(m); err != nil {
			emitDiagnostic("agent slim (bake): remove %s failed (non-fatal): %v", m, err)
		}
	}
}

func ensureGuestCLITools() (string, error) {
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

	// Keep bake minimal: install Node runtime via mise and run tools on-demand
	// through npx wrappers instead of global npm installs.
	misePath, err := ensureMiseInstalled()
	if err != nil {
		return "", fmt.Errorf("install mise: %w", err)
	}
	if err := ensureMiseNodeAvailable(misePath); err != nil {
		return "", fmt.Errorf("install node via mise: %w", err)
	}

	emitDiagnostic("agent cli tools: installing lightweight npx wrappers (on-demand package fetch)")
	if err := installNpxBinaryWrapper(misePath); err != nil {
		return "", fmt.Errorf("install npx wrapper: %w", err)
	}
	for _, tool := range tools {
		if err := installNpxWrapper(tool.binary, tool.pkg, misePath); err != nil {
			return "", fmt.Errorf("install wrapper for %s: %w", tool.binary, err)
		}
		emitDiagnostic("agent cli tools: installed wrapper %s", tool.binary)
	}

	emitDiagnostic("agent cli tools: installed successfully (total %v)", time.Since(start).Round(time.Second))
	return misePath, nil
}

func ensureMiseInstalled() (string, error) {
	if path, err := lookPathInEnv("mise", os.Environ()); err == nil && strings.TrimSpace(path) != "" {
		return path, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	install := exec.CommandContext(ctx, "sh", "-lc", "curl -fsSL https://mise.run | sh")
	install.Env = ensurePathInEnv(os.Environ())
	if out, err := install.CombinedOutput(); err != nil {
		return "", fmt.Errorf("installer failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if path, err := lookPathInEnv("mise", os.Environ()); err == nil && strings.TrimSpace(path) != "" {
		return path, nil
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		home = "/root"
	}
	candidate := filepath.Join(home, ".local", "bin", "mise")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", errors.New("mise binary not found after install")
}

func ensureMiseNodeAvailable(misePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, misePath, "x", "node@20", "--", "node", "--version")
	cmd.Env = ensurePathInEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("node@20 unavailable: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := ensureMiseNodeGlobalDefault(misePath); err != nil {
		return err
	}
	return nil
}

// ensureMiseNodeGlobalDefault sets node@20 as the mise global default if it
// hasn't been configured yet. This is idempotent and safe to call on every boot.
// It ensures the `node` shim resolves correctly for non-interactive shells and
// workspace exec commands.
func ensureMiseNodeGlobalDefault(misePath string) error {
	// Check if a global config already exists.
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		home = "/root"
	}
	configPath := filepath.Join(home, ".config", "mise", "config.toml")
	if _, err := os.Stat(configPath); err == nil {
		// Config exists — assume global default is already set.
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, misePath, "use", "-g", "node@20")
	cmd.Env = ensurePathInEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("set global node@20: %w: %s", err, strings.TrimSpace(string(out)))
	}
	emitDiagnostic("agent cli tools: set node@20 as mise global default")
	return nil
}

func installNpxWrapper(binary, npmPackage, misePath string) error {
	content := fmt.Sprintf("#!/usr/bin/env sh\nexec %s x node@20 -- npx -y %s \"$@\"\n", misePath, npmPackage)
	target := filepath.Join("/usr/local/bin", binary)
	if err := os.WriteFile(target, []byte(content), 0o755); err != nil {
		return err
	}
	// workspace exec shells may not include /usr/local/bin in PATH; mirror into /usr/bin.
	mirror := filepath.Join("/usr/bin", binary)
	if err := os.WriteFile(mirror, []byte(content), 0o755); err != nil {
		return err
	}
	return nil
}

func installNpxBinaryWrapper(misePath string) error {
	content := fmt.Sprintf("#!/usr/bin/env sh\nexec %s x node@20 -- npx \"$@\"\n", misePath)
	if err := os.WriteFile("/usr/local/bin/npx", []byte(content), 0o755); err != nil {
		return err
	}
	// workspace exec shells may not include /usr/local/bin in PATH; mirror into /usr/bin.
	return os.WriteFile("/usr/bin/npx", []byte(content), 0o755)
}

func toolchainPresentInPATH(env []string) bool {
	required := []string{"docker", "dockerd", "opencode", "codex", "claude"}
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

	// In libkrun hybrid mode: Docker data lives on /dev/vdc (mounted at
	// /var/lib/docker), a dedicated sparse ext4 so overlay2 runs on a native
	// kernel filesystem.
	// In legacy mode: data lives inside /workspace (native ext4 block device).
	dataRoot := "/workspace/.nexus-docker"
	execRoot := "/workspace/.nexus-docker-exec"
	socketPath := "/var/run/docker.sock"
	if hasDedicatedDockerDisk() {
		dataRoot = "/var/lib/docker"
		execRoot = "/var/lib/docker/exec"
		// In virtiofs-root mode (TSI), /run lives on the virtiofs root and
		// dockerd cannot chown a socket there; keep the real socket on
		// docker-data ext4 and expose /var/run/docker.sock as a symlink.
		// In hybrid block-root mode, /run is a writable tmpfs so the socket
		// can live at /var/run/docker.sock directly.
		if isVirtiofsWorkspaceMode() {
			socketPath = "/var/lib/docker/docker.sock"
		}
	}
	dockerHost := "unix://" + socketPath

	_ = os.Remove("/var/run/docker.pid")
	_ = os.Remove("/var/run/docker.sock")
	_ = os.Remove(socketPath)

	// Kill any stale containerd process left from a previous session.
	// dockerd manages its own containerd instance and stores the PID at a
	// well-known location inside exec-root.  If that process is still alive
	// (e.g. after a hard VM reboot) dockerd refuses to start because it
	// thinks containerd is already running.
	staleContainerdPIDs := []string{
		filepath.Join(execRoot, "containerd/containerd.pid"),
		"/var/run/docker/containerd/containerd.pid",
		"/run/docker/containerd/containerd.pid",
	}
	for _, pidFile := range staleContainerdPIDs {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			_ = os.Remove(pidFile)
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
			// Brief pause so the kernel can reap the process before dockerd starts.
			time.Sleep(200 * time.Millisecond)
		}
		_ = os.Remove(pidFile)
		emitDiagnostic("agent docker daemon: removed stale containerd pid file %s (pid %d)", pidFile, pid)
	}

	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir docker data root: %w", err)
	}
	if err := os.MkdirAll(execRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir docker exec root: %w", err)
	}
	if hasDedicatedDockerDisk() {
		// Remove any stale /run/containerd symlink left from a previous boot
		// (e.g. after switching workspace modes). Then redirect containerd's
		// default /run/containerd path into the exec-root on the docker-data
		// disk so that containerd socket/state survives across VM restarts.
		if info, err := os.Lstat("/run/containerd"); err == nil && info.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove("/run/containerd")
		}
		if err := ensureRunPathRedirect("/run/containerd", filepath.Join(execRoot, "run-containerd")); err != nil {
			emitDiagnostic("agent docker daemon: /run/containerd redirect failed (non-fatal): %v", err)
		}
	}

	// Probe NAT+RAW table support before enabling Docker's iptables integration.
	// Docker's bridge path installs rules in raw and nat; if either table is
	// unavailable we must disable iptables integration.
	iptablesFlag := "--iptables=false"
	if legacyPath, err := exec.LookPath("iptables-legacy"); err == nil {
		natProbe := exec.Command(legacyPath, "-w", "-t", "nat", "-L", "-n")
		rawProbe := exec.Command(legacyPath, "-w", "-t", "raw", "-L", "-n")
		if err := natProbe.Run(); err == nil && rawProbe.Run() == nil {
			_ = exec.Command("update-alternatives", "--set", "iptables", legacyPath).Run()
			if legacy6Path, err := exec.LookPath("ip6tables-legacy"); err == nil {
				_ = exec.Command("update-alternatives", "--set", "ip6tables", legacy6Path).Run()
			}
			iptablesFlag = "--iptables=true"
		} else {
			emitDiagnostic("agent docker daemon: iptables nat/raw unavailable; disabling Docker iptables integration")
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
			if hasDedicatedDockerDisk() && isVirtiofsWorkspaceMode() {
				_ = os.MkdirAll("/var/run", 0o755)
				_ = os.Remove("/var/run/docker.sock")
				_ = os.Symlink(socketPath, "/var/run/docker.sock")
			}
			_ = os.Setenv("DOCKER_HOST", dockerHost)
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
	// Use a 90s total budget for all network-setup subprocesses. Individual
	// DHCP clients have their own shorter timeouts, but a hung process (e.g.
	// dhcpcd stuck waiting on a missing virtio-net device) would block forever
	// without this outer deadline.
	netCtx, netCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer netCancel()

	// Enable IPv4 forwarding so Docker containers can reach external networks
	// even when Docker's iptables integration is disabled.
	emitDiagnostic("network: starting setup")
	_ = exec.CommandContext(netCtx, "sysctl", "-w", "net.ipv4.ip_forward=1").Run()

	emitDiagnostic("network: bringing up loopback")
	if out, err := exec.CommandContext(netCtx, "ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set lo up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if !interfaceExists("eth0") {
		if isTSINetworkMode() {
			emitDiagnostic("network: eth0 not present in TSI/no-NIC mode; skipping DHCP/static interface setup")
			return nil
		}
		return fmt.Errorf("network interface eth0 not found")
	}

	emitDiagnostic("network: eth0 found, bringing up interface")
	// Bring the interface up first so DHCP can configure it.
	if out, err := exec.CommandContext(netCtx, "ip", "link", "set", "eth0", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set eth0 up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if _, err := exec.LookPath("udhcpc"); err == nil {
		// Run udhcpc in one-shot mode: -n (exit on failure), -q (quit after obtaining
		// lease), -t 10 (retry 10 times), -T 3 (3s between retries) -> max ~30s wait.
		emitDiagnostic("network: running udhcpc DHCP (max 30s)")
		out, err := exec.CommandContext(netCtx,
			"udhcpc", "-i", "eth0", "-n", "-q", "-t", "10", "-T", "3",
		).CombinedOutput()
		if err == nil {
			if hasUsableIPv4OnEth0() {
				emitDiagnostic("network: udhcpc configured eth0 successfully")
				return nil
			}
			emitDiagnostic("udhcpc exited successfully but eth0 has no usable IPv4; falling through to other DHCP/static methods")
		} else {
			emitDiagnostic("udhcpc failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
	} else if _, err := exec.LookPath("dhcpcd"); err == nil {
		// dhcpcd is available (installed on Ubuntu when busybox is not).
		// -1 (--oneshot): exit after the first interface is configured.
		// This makes dhcpcd behave like a one-shot DHCP client.
		emitDiagnostic("network: running dhcpcd DHCP (max 45s)")
		dhcpctx, dhcpcancel := context.WithTimeout(netCtx, 45*time.Second)
		out, err := exec.CommandContext(dhcpctx, "dhcpcd", "-1", "eth0").CombinedOutput()
		dhcpcancel()
		if err == nil {
			if hasUsableIPv4OnEth0() {
				emitDiagnostic("network: dhcpcd configured eth0 successfully")
				return nil
			}
			emitDiagnostic("dhcpcd exited successfully but eth0 has no usable IPv4; falling through to static fallback")
		} else {
			emitDiagnostic("dhcpcd failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
	} else if _, err := exec.LookPath("dhclient"); err == nil {
		emitDiagnostic("network: running dhclient DHCP (max 45s)")
		dhcpctx, dhcpcancel := context.WithTimeout(netCtx, 45*time.Second)
		out, err := exec.CommandContext(dhcpctx, "dhclient", "-1", "-v", "eth0").CombinedOutput()
		dhcpcancel()
		if err == nil {
			if hasUsableIPv4OnEth0() {
				emitDiagnostic("network: dhclient configured eth0 successfully")
				return nil
			}
			emitDiagnostic("dhclient exited successfully but eth0 has no usable IPv4; falling through to static fallback")
		} else {
			emitDiagnostic("dhclient failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}

	emitDiagnostic("network: all DHCP clients failed or unavailable, falling back to static guest IP")
	if err := configureStaticGuestNetwork(); err != nil {
		return fmt.Errorf("static network fallback failed: %w", err)
	}
	if !hasUsableIPv4OnEth0() {
		return fmt.Errorf("static guest IP applied but eth0 still has no routable IPv4 or default via gateway on eth0")
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

// verifyBakeOutboundReachable ensures virtio-net bake VMs can reach Ubuntu mirrors
// before apt runs, so a broken passt/DHCP setup fails in seconds instead of
// hanging on "Connecting to archive.ubuntu.com".
func verifyBakeOutboundReachable() error {
	if isTSINetworkMode() {
		return nil
	}
	if !hasUsableIPv4OnEth0() {
		return fmt.Errorf("eth0 has no routable IPv4 or default route via gateway (check passt / --address)")
	}
	conn, err := net.DialTimeout("tcp", "archive.ubuntu.com:80", 12*time.Second)
	if err != nil {
		return fmt.Errorf("cannot reach archive.ubuntu.com:80 for apt (outbound blocked or DNS broken): %w", err)
	}
	_ = conn.Close()
	return nil
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
	gwTrim := strings.TrimSpace(gateway)
	gwParts := strings.SplitN(gwTrim, ".", 4)
	if len(gwParts) < 2 {
		return fmt.Sprintf("172.26.%d.%d", b4, b5), nil
	}
	ip := fmt.Sprintf("%s.%s.%d.%d", gwParts[0], gwParts[1], b4, b5)
	if ip == gwTrim {
		// When the last two MAC octets match the gateway's host bits (e.g. bake
		// workspace MAC → 192.168.44.1 with gateway 192.168.44.1), passt rejects
		// --address and the guest never gets usable DHCP — shift host bits.
		ip = fmt.Sprintf("%s.%s.%d.%d", gwParts[0], gwParts[1], (int(b4)+131)%256, (int(b5)+73)%256)
	}
	if ip == gwTrim {
		return fmt.Sprintf("172.26.%d.%d", b4, b5), nil
	}
	return ip, nil
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

// hasDedicatedDockerDisk reports whether the host has provided a dedicated
// docker-data block device (NEXUS_DOCKER_DEV env var set by launchHybridMode).
// When true, docker data/exec root live at /var/lib/docker (mounted from the
// block device) rather than inside /workspace.
func hasDedicatedDockerDisk() bool {
	return os.Getenv("NEXUS_DOCKER_DEV") != ""
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

// prepullBakedDockerImages starts dockerd temporarily during bake mode,
// pulls commonly-used Docker images, then stops dockerd. This ensures the
// images are present in the baked rootfs so CI environments (which may not
// have Docker Hub access) can use them without pulling.
// Failure is non-fatal — bake continues without pre-pulled images.
func prepullBakedDockerImages() error {
	emitDiagnostic("agent bake: pre-pulling Docker images into rootfs")

	// Use /workspace for docker data (the workspace ext4 is writable during bake).
	dataRoot := "/workspace/.nexus-docker-bake"
	execRoot := "/workspace/.nexus-docker-bake-exec"
	socketPath := "/var/run/docker.sock"

	_ = os.Remove(socketPath)
	_ = os.MkdirAll(dataRoot, 0o755)
	_ = os.MkdirAll(execRoot, 0o755)

	// Start dockerd with minimal flags — no iptables needed for pull.
	logFile, err := os.OpenFile("/tmp/nexus-agent-dockerd-bake.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open dockerd bake log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command("dockerd",
		"--host=unix://"+socketPath,
		"--data-root="+dataRoot,
		"--exec-root="+execRoot,
		"--storage-driver=overlay2",
		"--iptables=false",
		"--bridge=none",
	)
	cmd.Env = append(ensurePathInEnv(os.Environ()), "DOCKER_INSECURE_NO_IPTABLES_RAW=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dockerd for bake: %w", err)
	}
	_ = cmd.Process.Release()

	// Wait for dockerd to be ready.
	dockerEnv := append(ensurePathInEnv(os.Environ()), "DOCKER_HOST=unix://"+socketPath)
	ready := false
	for i := 0; i < 30; i++ {
		check := exec.Command("docker", "info")
		check.Env = dockerEnv
		if check.Run() == nil {
			ready = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !ready {
		return fmt.Errorf("dockerd did not become ready within 30s during bake")
	}
	emitDiagnostic("agent bake: dockerd ready for image pre-pull")

	// Pull images with retry.
	images := []string{"nginx:alpine", "alpine"}
	for _, image := range images {
		emitDiagnostic("agent bake: pulling %s", image)
		var pullErr error
		for attempt := 1; attempt <= 3; attempt++ {
			if attempt > 1 {
				emitDiagnostic("agent bake: retrying pull %s (%d/3)", image, attempt)
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
			}
			pullCmd := exec.Command("docker", "pull", image)
			pullCmd.Env = dockerEnv
			if out, err := pullCmd.CombinedOutput(); err == nil {
				emitDiagnostic("agent bake: pulled %s OK", image)
				pullErr = nil
				break
			} else {
				pullErr = fmt.Errorf("pull %s: %w: %s", image, err, strings.TrimSpace(string(out)))
			}
		}
		if pullErr != nil {
			emitDiagnostic("agent bake: WARNING — failed to pull %s (non-fatal): %v", image, pullErr)
		}
	}

	// Stop dockerd gracefully.
	emitDiagnostic("agent bake: stopping dockerd after image pre-pull")
	stopCmd := exec.Command("docker", "-H", "unix://"+socketPath, "info")
	stopCmd.Env = dockerEnv
	if stopCmd.Run() == nil {
		// Send SIGTERM to dockerd for clean shutdown.
		_ = exec.Command("pkill", "-TERM", "dockerd").Run()
		time.Sleep(3 * time.Second)
	}
	_ = os.Remove(socketPath)

	emitDiagnostic("agent bake: Docker image pre-pull complete")
	return nil
}
