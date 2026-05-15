//go:build darwin

package macvm

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
	vmnet "github.com/oursky/nexus/packages/nexus/internal/vm/net"
)

// BakeMacConfig configures a macOS host rootfs bake (Hypervisor.framework + gvproxy).
type BakeMacConfig struct {
	// LibDir hosts libkrun.dylib / libkrunfw.dylib.
	LibDir string
	// RootFSBasePath is the uncompressed ext4 to clone and bake (replaced on success).
	RootFSBasePath string
	// EmbeddedAgentFn injects bytes into /usr/local/bin/nexus-guest-agent before bake.
	EmbeddedAgentFn func() []byte
}

func bakeMacTimeout() time.Duration {
	const defaultTimeout = 55 * time.Minute
	raw := strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_BAKE_TIMEOUT"))
	if raw == "" {
		return defaultTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("[macvm] rootfs bake: invalid NEXUS_LIBKRUN_BAKE_TIMEOUT=%q; using %s", raw, defaultTimeout)
		return defaultTimeout
	}
	return d
}

func bakeMacMaxAttempts() int {
	const defaultAttempts = 1
	raw := strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS"))
	if raw == "" {
		return defaultAttempts
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		log.Printf("[macvm] rootfs bake: invalid NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=%q; using %d", raw, defaultAttempts)
		return defaultAttempts
	}
	return n
}

// BakeRootfsIfNeeded boots a transient macVM with gvproxy-backed outbound networking,
// runs the embedded guest-agent in bake mode, and atomically replaces RootFSBasePath.
func BakeRootfsIfNeeded(ctx context.Context, cfg BakeMacConfig, stampDir string) error {
	cfg.LibDir = strings.TrimSpace(cfg.LibDir)
	if cfg.LibDir == "" {
		return fmt.Errorf("macvm bake: LibDir not set — cannot bake")
	}
	if strings.TrimSpace(cfg.RootFSBasePath) == "" {
		return fmt.Errorf("macvm bake: RootFSBasePath not set")
	}
	stampPath := filepath.Join(stampDir, "rootfs-baked-"+HostBakeStampVersion)
	if _, err := os.Stat(stampPath); err == nil {
		log.Printf("[macvm] rootfs bake: already baked (stamp %s)", stampPath)
		return nil
	}

	if _, err := os.Stat(cfg.RootFSBasePath); err != nil {
		return fmt.Errorf("macvm bake: base rootfs not found: %w", err)
	}

	if resolveMkfsExt4() == "" {
		return fmt.Errorf("macvm bake: mkfs.ext4 not found (brew install e2fsprogs)")
	}
	if resolveDebugFS() == "" {
		return fmt.Errorf("macvm bake: debugfs not found (brew install e2fsprogs)")
	}

	if rootfsHasBakeStamp(cfg.RootFSBasePath) {
		if err := os.MkdirAll(stampDir, 0o755); err == nil {
			_ = os.WriteFile(stampPath, []byte("baked\n"), 0o644)
		}
		log.Printf("[macvm] rootfs bake: in-image stamp detected in %s; restored host stamp %s",
			cfg.RootFSBasePath, stampPath)
		return nil
	}

	timeout := bakeMacTimeout()
	maxAttempts := bakeMacMaxAttempts()
	log.Printf("[macvm] rootfs bake: installing tools timeout=%s max_attempts=%d", timeout, maxAttempts)

	bakeCtx, bakeCancel := context.WithTimeout(ctx, timeout)
	defer bakeCancel()

	var bakedPath string
	var bakeErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			log.Printf("[macvm] rootfs bake: retrying bake (attempt %d/%d)...", attempt, maxAttempts)
		}
		cleanupStaleMacBakeArtifacts()
		bakedPath, bakeErr = runBakeMacVM(bakeCtx, cfg)
		if bakeErr == nil {
			break
		}
		log.Printf("[macvm] rootfs bake: attempt %d failed: %v", attempt, bakeErr)
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
	}
	if bakeErr != nil {
		return fmt.Errorf("macvm bake VM failed after %d attempt(s): %w", maxAttempts, bakeErr)
	}
	defer func() { _ = os.Remove(bakedPath) }()

	tmpDest := cfg.RootFSBasePath + ".bake-new"
	if err := copyFile(bakedPath, tmpDest); err != nil {
		return fmt.Errorf("macvm bake: copy baked rootfs: %w", err)
	}
	if err := os.Rename(tmpDest, cfg.RootFSBasePath); err != nil {
		_ = os.Remove(tmpDest)
		return fmt.Errorf("macvm bake: replace base rootfs: %w", err)
	}

	if err := os.MkdirAll(stampDir, 0o755); err == nil {
		_ = os.WriteFile(stampPath, []byte("baked\n"), 0o644)
	}
	log.Printf("[macvm] rootfs bake: complete — base rootfs baked at %s", cfg.RootFSBasePath)
	return nil
}

func cleanupStaleMacBakeArtifacts() {
	bases := []string{"/tmp", os.TempDir()}
	patterns := []string{"nxmb-*", "nexus-macvm-bake-*"}
	seen := make(map[string]struct{})
	for _, base := range bases {
		for _, pat := range patterns {
			matches, _ := filepath.Glob(filepath.Join(base, pat))
			for _, dir := range matches {
				if _, dup := seen[dir]; dup {
					continue
				}
				seen[dir] = struct{}{}
				if err := os.RemoveAll(dir); err != nil {
					log.Printf("[macvm] bake cleanup: remove stale bake dir %s: %v", dir, err)
				}
			}
		}
	}
}

func readSerialSuccess(serialLog string) (ok bool, failed bool, data string) {
	b, err := os.ReadFile(serialLog)
	if err != nil || len(b) == 0 {
		return false, false, ""
	}
	s := string(b)
	return strings.Contains(s, "agent bake: all tools installed"),
		strings.Contains(s, "agent bake: FAILED"),
		s
}

func relocateBakedRootfs(rootfsPath string) (string, error) {
	tmp, err := os.CreateTemp("", "nexus-baked-rootfs-*.ext4")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(rootfsPath, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("relocate baked rootfs: %w", err)
	}
	return tmpPath, nil
}

func finishBakedRootfs(rootfsPath, serialLog string) (string, error) {
	bakeOK, bakeFail, tail := readSerialSuccess(serialLog)
	if bakeFail || !bakeOK {
		lines := strings.Split(tail, "\n")
		if len(lines) > 100 {
			lines = lines[len(lines)-100:]
		}
		log.Printf("[macvm] bake VM: serial tail:\n%s", strings.Join(lines, "\n"))
		return "", fmt.Errorf("macvm bake: tools-installed marker missing in serial log")
	}

	if ex := resolveE2fsck(); ex != "" {
		_ = exec.Command(ex, "-f", "-y", rootfsPath).Run()
	}
	if wr := writeStampIntoRootfs(rootfsPath); wr != nil {
		log.Printf("[macvm] bake VM: stamp write warning: %v", wr)
	}
	if symErr := ensureNPMBinSymlinksInRootfs(rootfsPath); symErr != nil {
		log.Printf("[macvm] bake VM: symlink repair warning: %v", symErr)
	}
	return relocateBakedRootfs(rootfsPath)
}

func bakeRunnerDiag(runnerLogPath string) string {
	const maxBytes = 16000
	b, err := os.ReadFile(runnerLogPath)
	if err != nil {
		return fmt.Sprintf("\n(no runner log at %s: %v)", runnerLogPath, err)
	}
	s := strings.TrimRight(string(b), "\n")
	if len(s) > maxBytes {
		s = "…(truncated)\n" + s[len(s)-maxBytes:]
	}
	return "\n--- macvm-runner log ---\n" + s
}

func runBakeMacVM(ctx context.Context, cfg BakeMacConfig) (string, error) {
	raiseSpawnFDLimits()

	workDir, err := macvmBakeWorkDir()
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	rootfsPath := filepath.Join(workDir, "rootfs.ext4")
	if err := copyFile(cfg.RootFSBasePath, rootfsPath); err != nil {
		return "", fmt.Errorf("clone rootfs: %w", err)
	}
	if cfg.EmbeddedAgentFn != nil {
		if agentData := cfg.EmbeddedAgentFn(); len(agentData) > 0 {
			if injectErr := injectFileIntoExt4(rootfsPath, agentData, "/usr/local/bin/nexus-guest-agent", 0o755); injectErr != nil {
				log.Printf("[macvm] bake: agent inject warning: %v", injectErr)
			}
		}
	}

	workspaceHostDir := filepath.Join(workDir, "workspace-host")
	if err := os.MkdirAll(workspaceHostDir, 0o755); err != nil {
		return "", fmt.Errorf("bake workspace host dir: %w", err)
	}

	sockDir, err := socketTempDir("mb-")
	if err != nil {
		return "", fmt.Errorf("macvm bake sock dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()

	sshHostPort, err := pickTCPPort127()
	if err != nil {
		return "", err
	}
	agentFwdPort, err := pickTCPPort127()
	if err != nil {
		return "", err
	}

	gvproxyBin, err := vmnet.FindGVProxy(true)
	if err != nil {
		return "", fmt.Errorf("gvproxy: %w", err)
	}
	sockGV := filepath.Join(sockDir, "gvproxy.sock")
	gvLogPath := filepath.Join(workDir, "gvproxy.log")
	gvp, err := vmnet.StartGVProxy(gvproxyBin, sockGV, gvLogPath)
	if err != nil {
		return "", fmt.Errorf("gvproxy start: %w", err)
	}
	defer func() { _ = gvp.Stop() }()

	if err := gvp.ExposeTCPForward(sshHostPort, guestSSHPortGuest); err != nil {
		return "", fmt.Errorf("gvproxy ssh tunnel: %w", err)
	}
	if err := gvp.ExposeTCPForward(agentFwdPort, guestAgentTCPPort); err != nil {
		return "", fmt.Errorf("gvproxy agent tunnel: %w", err)
	}

	var customKernelPath string
	var customKernelFormat uint32 = libkrun.KernelFormatRaw
	if strings.TrimSpace(os.Getenv("NEXUS_MACVM_BAKE_EMBEDDED_KERNEL_ONLY")) != "1" {
		for _, kp := range customKernelCandidates(cfg.LibDir, workDir) {
			if _, statErr := os.Stat(kp); statErr == nil {
				customKernelPath = kp
				if runtime.GOARCH == "amd64" {
					customKernelFormat = libkrun.KernelFormatElf
				}
				break
			}
		}
	}

	guestEnv := []string{
		"NEXUS_CONTAINER_MODE=1",
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		"NEXUS_BAKE=1",
		"AGENT_REQUIRE_VSOCK=1",
		fmt.Sprintf("AGENT_PORT=%d", guestAgentTCPPort),
		// GH Actions macOS runners return VmCreate EINVAL when attaching a dedicated workspace ext4
		// virtio-blk alongside rootfs (even without docker-data). Bake uses virtiofs "nexus-workspace"
		// instead; Docker prepull writes under /workspace only (sysconfig.go).
		"NEXUS_WORKSPACE_MODE=virtiofs",
		"NEXUS_VIRTIOFS_SKIP_DOCKER=1",
	}

	runnerCfg := macVMRunnerConfig{
		LibkrunPath:        filepath.Join(cfg.LibDir, "libkrun.dylib"),
		LibkrunfwPath:      filepath.Join(cfg.LibDir, "libkrunfw.dylib"),
		WorkspaceID:        "rootfs-bake",
		RootFSPath:         rootfsPath,
		DockerDataPath:     "",
		WorkspacePath:      workspaceHostDir,
		WorkspaceDiskPath:  "",
		ConfigDir:          "",
		SockDir:            sockDir,
		GVProxySockPath:    sockGV,
		MemMiB:             memMiBForEnv(),
		VCPUs:              vcpusForEnv(),
		CustomKernelPath:   customKernelPath,
		CustomKernelFormat: customKernelFormat,
		AgentPath:          "/usr/local/bin/nexus-guest-agent",
		GuestEnv:           guestEnv,
		LogLevel:           1,
	}

	runnerCfgPath := filepath.Join(sockDir, "vmrunner-bake.json")
	if err := writeMacVMRunnerConfig(runnerCfgPath, runnerCfg); err != nil {
		return "", err
	}

	serialSock := filepath.Join(sockDir, "hvc0.sock")
	serialLog := filepath.Join(workDir, "bake.serial.log")

	stopCapture := startConsoleSocketCapture(serialSock, serialLog)
	defer stopCapture()

	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}

	runnerLogPath := filepath.Join(workDir, "runner.log")
	logFile, logErr := os.Create(runnerLogPath)

	childCmd := exec.CommandContext(ctx, execPath, "_macvm-runner", runnerCfgPath) //nolint:forbidigo
	if logErr == nil {
		childCmd.Stdout = logFile
		childCmd.Stderr = logFile
	} else {
		childCmd.Stdout = io.Discard
		childCmd.Stderr = io.Discard
	}
	childCmd.Env = os.Environ()

	if err := childCmd.Start(); err != nil {
		return "", fmt.Errorf("start bake vm: %w", err)
	}
	if logFile != nil {
		_ = logFile.Close()
	}

	childDone := make(chan struct{})
	go func() {
		_ = childCmd.Wait()
		close(childDone)
	}()

	waitDone := make(chan error, 1)
	go func() { waitDone <- waitAgentListening(ctx, sockDir) }()

	select {
	case err := <-waitDone:
		if err != nil {
			_ = childCmd.Process.Kill()
			_ = gvp.Stop()
			return "", fmt.Errorf("bake VM agent not reachable: %w%s", err, bakeRunnerDiag(runnerLogPath))
		}
	case <-childDone:
		_ = childCmd.Process.Kill()
		_ = gvp.Stop()
		return "", fmt.Errorf("macvm-runner exited before guest agent socket was ready%s", bakeRunnerDiag(runnerLogPath))
	case <-ctx.Done():
		_ = childCmd.Process.Kill()
		_ = gvp.Stop()
		return "", fmt.Errorf("macvm bake cancelled waiting for agent: %w%s", ctx.Err(), bakeRunnerDiag(runnerLogPath))
	}

	start := time.Now()
	bakeTimeoutDur := bakeMacTimeout()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastHeartbeat := start
	const heartbeatEvery = 90 * time.Second

	for {
		select {
		case <-childDone:
			_, bakeFail, _ := readSerialSuccess(serialLog)
			if bakeFail {
				return "", fmt.Errorf("macvm bake VM failed inside guest (see %s)", serialLog)
			}
			return finishBakedRootfs(rootfsPath, serialLog)

		case <-ticker.C:
			elapsed := time.Since(start)
			if time.Since(lastHeartbeat) >= heartbeatEvery {
				log.Printf("[macvm] bake VM running (%v) — tail %s", elapsed.Round(time.Second), serialLog)
				lastHeartbeat = time.Now()
			}

			_, bakeFail, serialData := readSerialSuccess(serialLog)
			if bakeFail {
				_ = childCmd.Process.Kill()
				return "", fmt.Errorf("macvm bake: guest agent reported failure — see %s", serialLog)
			}

			if strings.Contains(serialData, "agent bake: all tools installed") {
				log.Printf("[macvm] bake VM: success marker at %v — waiting for graceful exit", elapsed.Round(time.Second))
				waitPoweroff(childCmd, childDone, gvp, 90*time.Second)
				_, bakeFail2, _ := readSerialSuccess(serialLog)
				if bakeFail2 {
					return "", fmt.Errorf("macvm bake failed during shutdown — see %s", serialLog)
				}
				return finishBakedRootfs(rootfsPath, serialLog)
			}

			if elapsed > bakeTimeoutDur {
				_ = childCmd.Process.Kill()
				_ = gvp.Stop()
				return "", fmt.Errorf("macvm bake timed out after %v (serial %s)", bakeTimeoutDur, serialLog)
			}

		case <-ctx.Done():
			_ = childCmd.Process.Kill()
			return "", ctx.Err()
		}
	}
}

func waitPoweroff(cmd *exec.Cmd, childDone <-chan struct{}, gvp *vmnet.GVProxy, maxWait time.Duration) {
	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-childDone:
			return
		case <-timer.C:
			log.Printf("[macvm] bake VM did not exit in %v — forcing kill", maxWait)
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			if gvp != nil {
				_ = gvp.Stop()
			}
			return
		case <-ticker.C:
			// still waiting for guest ACPI power-off
		}
	}
}
