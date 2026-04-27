//go:build linux

package libkrun

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func bakeTimeout() time.Duration {
	const defaultTimeout = 12 * time.Minute
	raw := strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_BAKE_TIMEOUT"))
	if raw == "" {
		return defaultTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("[libkrun] rootfs bake: invalid NEXUS_LIBKRUN_BAKE_TIMEOUT=%q; using %s", raw, defaultTimeout)
		return defaultTimeout
	}
	return d
}

func bakeMaxAttempts() int {
	const defaultAttempts = 1
	raw := strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS"))
	if raw == "" {
		return defaultAttempts
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		log.Printf("[libkrun] rootfs bake: invalid NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS=%q; using %d", raw, defaultAttempts)
		return defaultAttempts
	}
	return n
}

// BakeRootfsIfNeeded checks whether the base rootfs already has developer
// tools pre-installed (stamp present). If not, it boots a temporary VM in
// "bake mode": the agent installs packages + npm tools synchronously then
// powers off the VM. The resulting rootfs becomes the new base so every
// subsequent workspace clone starts with tools already present.
//
// stampDir is the directory to store the host-side bake stamp
// (e.g. ~/.local/state/nexus). Non-fatal: logs a warning and returns nil
// on failure so daemon start is never blocked.
func BakeRootfsIfNeeded(ctx context.Context, cfg ManagerConfig, stampDir string) error {
	stampPath := filepath.Join(stampDir, "rootfs-baked-"+BakeStampVersion)
	if _, err := os.Stat(stampPath); err == nil {
		log.Printf("[libkrun] rootfs bake: already baked (stamp %s)", stampPath)
		return nil
	}
	// Backward-compat: older host caches used v6/v5/v4 bake-stamp names.
	for _, legacy := range []string{"v6", "v5", "v4"} {
		legacyStamp := filepath.Join(stampDir, "rootfs-baked-"+legacy)
		if _, err := os.Stat(legacyStamp); err == nil {
			if err := os.MkdirAll(stampDir, 0o755); err == nil {
				_ = os.WriteFile(stampPath, []byte("baked\n"), 0o644)
			}
			log.Printf("[libkrun] rootfs bake: promoted legacy stamp %s -> %s; skipping rebake", legacyStamp, stampPath)
			return nil
		}
	}

	if cfg.LibkrunVMBin == "" {
		return fmt.Errorf("LibkrunVMBin not set — cannot bake rootfs")
	}
	if cfg.RootFSBasePath == "" {
		return fmt.Errorf("RootFSBasePath not set — cannot bake rootfs")
	}
	if _, err := os.Stat(cfg.RootFSBasePath); err != nil {
		return fmt.Errorf("base rootfs not found at %s: %w", cfg.RootFSBasePath, err)
	}
	// If the host-side stamp is missing but the rootfs itself still has the
	// in-image tools stamp, restore the host stamp and skip the expensive bake.
	if rootfsHasBakeStamp(cfg.RootFSBasePath) {
		if err := os.MkdirAll(stampDir, 0o755); err == nil {
			_ = os.WriteFile(stampPath, []byte("baked\n"), 0o644)
		}
		log.Printf("[libkrun] rootfs bake: in-image stamp detected in %s; restored host stamp %s and skipped rebake", cfg.RootFSBasePath, stampPath)
		return nil
	}

	timeout := bakeTimeout()
	maxAttempts := bakeMaxAttempts()
	log.Printf("[libkrun] rootfs bake: pre-installing tools with timeout=%s max_attempts=%d", timeout, maxAttempts)

	var bakedRootfsPath string
	var bakeErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			log.Printf("[libkrun] rootfs bake: retrying bake (attempt %d/%d)...", attempt, maxAttempts)
		}
		bakedRootfsPath, bakeErr = runBakeVM(ctx, cfg)
		if bakeErr == nil {
			break
		}
		log.Printf("[libkrun] rootfs bake: attempt %d failed: %v", attempt, bakeErr)
		if attempt < maxAttempts {
			// Wait before retrying — network/DNS issues may be transient.
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
	}
	if bakeErr != nil {
		return fmt.Errorf("bake VM failed after %d attempt(s): %w", maxAttempts, bakeErr)
	}
	// Always remove the temp baked rootfs when we're done with it.
	defer os.Remove(bakedRootfsPath)

	// Atomically replace the base rootfs with the baked version.
	tmpDest := cfg.RootFSBasePath + ".bake-new"
	if err := copyFile(bakedRootfsPath, tmpDest); err != nil {
		return fmt.Errorf("copy baked rootfs: %w", err)
	}
	if err := os.Rename(tmpDest, cfg.RootFSBasePath); err != nil {
		_ = os.Remove(tmpDest)
		return fmt.Errorf("replace base rootfs: %w", err)
	}

	// Write bake stamp so subsequent daemon starts skip baking.
	if err := os.MkdirAll(stampDir, 0o755); err == nil {
		_ = os.WriteFile(stampPath, []byte("baked\n"), 0o644)
	}

	log.Printf("[libkrun] rootfs bake: complete — all future workspace clones will start with tools pre-installed")
	return nil
}

func rootfsHasBakeStamp(rootfsPath string) bool {
	const stampInsideRootfs = "/var/lib/nexus-tools-base-v7"
	if strings.TrimSpace(rootfsPath) == "" {
		return false
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return false
	}
	out, err := exec.Command("debugfs", "-R", "stat "+stampInsideRootfs, rootfsPath).CombinedOutput()
	joined := strings.ToLower(string(out))
	// debugfs exits 0 even when the file is not found; treat "not found" as
	// missing regardless of exit code.
	if strings.Contains(joined, "file not found") || strings.Contains(joined, "not found by ext2_lookup") {
		return false
	}
	if err == nil {
		return true
	}
	// debugfs can emit warnings to stderr while still producing usable output.
	// Treat explicit inode matches as success.
	return strings.Contains(joined, "inode:")
}

// DeleteBakeStamp removes the host-side bake stamp so the next daemon start
// triggers a fresh bake. Called when the base rootfs is rebuilt from scratch.
func DeleteBakeStamp(stampDir string) {
	_ = os.Remove(filepath.Join(stampDir, "rootfs-baked-"+BakeStampVersion))
}

// IsRootfsBaked reports whether the host-side bake stamp exists.
func IsRootfsBaked(stampDir string) bool {
	stampPath := filepath.Join(stampDir, "rootfs-baked-"+BakeStampVersion)
	_, err := os.Stat(stampPath)
	return err == nil
}

// runBakeVM boots a temporary libkrun VM with nexus.bake=1 in the kernel
// cmdline. The agent installs all packages/tools and then powers off the VM.
// Returns the path to the baked rootfs.ext4; the caller is responsible for
// removing it when done (it is a temp file outside the bake workdir).
func runBakeVM(ctx context.Context, cfg ManagerConfig) (string, error) {
	workDir, err := os.MkdirTemp("", "nexus-rootfs-bake-*")
	if err != nil {
		return "", fmt.Errorf("create temp workdir: %w", err)
	}
	defer os.RemoveAll(workDir) // safe: rootfs is renamed out before return

	// Clone base rootfs — the bake VM will write to this copy.
	rootfsPath := filepath.Join(workDir, "rootfs.ext4")
	if err := copyFile(cfg.RootFSBasePath, rootfsPath); err != nil {
		return "", fmt.Errorf("clone base rootfs: %w", err)
	}

	// Inject the current embedded agent so the bake VM runs the latest code.
	if cfg.EmbeddedAgentFn != nil {
		if agentData := cfg.EmbeddedAgentFn(); len(agentData) > 0 {
			if err := injectFileIntoExt4(rootfsPath, agentData, "/usr/local/bin/nexus-guest-agent", 0o755); err != nil {
				log.Printf("[libkrun] bake: agent inject warning: %v", err)
			}
		}
	}

	// Minimal workspace ext4 — just needs to satisfy /dev/vdb mount.
	workspacePath := filepath.Join(workDir, "workspace.ext4")
	if err := createBakeWorkspaceImage(workspacePath); err != nil {
		return "", fmt.Errorf("create bake workspace image: %w", err)
	}

	// Docker data ext4 (sparse) — needed if ensureDockerDaemon runs.
	dockerDataPath := filepath.Join(workDir, "docker-data.ext4")
	if err := createDockerDataImage(dockerDataPath); err != nil {
		return "", fmt.Errorf("create bake docker-data image: %w", err)
	}

	serialLog := filepath.Join(workDir, "bake.log")

	// The bake VM needs outbound internet access to download packages from
	// Ubuntu repositories. We use virtio-net + passt (same as regular VMs)
	// instead of TSI, because TSI only provides local port forwarding and
	// does not proxy arbitrary outbound TCP connections.
	sshPort, err := pickFreePort()
	if err != nil {
		return "", fmt.Errorf("pick free port: %w", err)
	}

	log.Printf("[libkrun] bake VM: starting passt for outbound internet access")
	vmSideFD, hostSideFD, err := createPasstSocketPair()
	if err != nil {
		return "", fmt.Errorf("create passt socketpair: %w", err)
	}
	// NOTE: Do NOT use a timeout context for passt — it is a daemon that runs
	// for the lifetime of the VM. Using a timeout would kill it prematurely.
	passtProc, err := startPasstProcess(ctx, workDir, hostSideFD, sshPort, passtGuestIPv4ForWorkspace("rootfs-bake"))
	if err != nil {
		_ = vmSideFD.Close()
		_ = hostSideFD.Close()
		return "", fmt.Errorf("start passt: %w", err)
	}
	// NOTE: Do NOT close hostSideFD here — passt owns it via ExtraFiles.
	// It will be closed after childCmd starts (same pattern as manager.go).

	kernelCmdline := "console=hvc0 root=/dev/vda rw init=/usr/local/bin/nexus-guest-agent nexus.bake=1"
	if gw := strings.TrimSpace(hostDefaultGatewayIP()); gw != "" {
		kernelCmdline += " nexus.gw=" + gw
	}
	if dns := strings.Join(hostDNSServers(), ","); dns != "" {
		kernelCmdline += " nexus.dns=" + dns
	}

	vmSpec := VMSpec{
		WorkspaceID:     "rootfs-bake",
		KernelPath:      cfg.KernelPath,
		KernelCmdline:   kernelCmdline,
		RootFSImage:     rootfsPath,
		AgentPath:       "/usr/local/bin/nexus-guest-agent",
		WorkspaceImage:  workspacePath,
		DockerDataImage: dockerDataPath,
		MemoryMiB:       1024,
		VCPUs:           2,
		SerialLog:       serialLog,
		NetworkBackend:  "virtio-net",
		PortMap:         []string{fmt.Sprintf("%d:22", sshPort)},
		PasstFDIndex:    0,
		LibkrunLogLevel: 1, // errors only — suppress the DEBUG interrupt flood
	}

	specPath := filepath.Join(workDir, "libkrun-bake.json")
	specData, err := json.Marshal(vmSpec)
	if err != nil {
		return "", fmt.Errorf("marshal bake VMSpec: %w", err)
	}
	if err := os.WriteFile(specPath, specData, 0o600); err != nil {
		return "", fmt.Errorf("write bake VMSpec: %w", err)
	}

	libDir := filepath.Join(filepath.Dir(cfg.LibkrunVMBin), "..", "lib")
	env := append(os.Environ(), "LD_LIBRARY_PATH="+libDir+":"+os.Getenv("LD_LIBRARY_PATH"))

	childCmd := exec.CommandContext(ctx, cfg.LibkrunVMBin, "--config="+specPath)
	childCmd.Env = env
	childCmd.Dir = workDir
	childCmd.ExtraFiles = []*os.File{vmSideFD}

	// Redirect bake VM stdout/stderr to the log file rather than the operator's
	// terminal — libkrun emits thousands of interrupt lines even at lower log
	// levels. Progress is visible in serialLog+".hvc0" (agent serial output).
	vmLogFile, logErr := os.OpenFile(serialLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if logErr != nil {
		vmLogFile = nil
	}
	if vmLogFile != nil {
		childCmd.Stdout = vmLogFile
		childCmd.Stderr = vmLogFile
	}

	log.Printf("[libkrun] bake VM: starting — guest bootstrap in progress")

	if err := childCmd.Start(); err != nil {
		if passtProc != nil {
			_ = passtProc.Kill()
		}
		_ = vmSideFD.Close()
		if vmLogFile != nil {
			_ = vmLogFile.Close()
		}
		return "", fmt.Errorf("start bake VM: %w", err)
	}
	_ = vmSideFD.Close()
	_ = hostSideFD.Close()
	if vmLogFile != nil {
		_ = vmLogFile.Close()
	}

	// Reap the child in a goroutine. We don't block on it — libkrun's VMM
	// cleanup can hang for minutes after the guest poweroff. Instead we poll
	// the hvc0 serial log for the completion marker, then kill the process.
	childDone := make(chan struct{})
	go func() {
		_ = childCmd.Wait()
		close(childDone)
	}()

	bakeTimeout := bakeTimeout()
	hvc0Path := serialLog + ".hvc0"
	start := time.Now()

	// Poll every 5 s for the agent's "System halted" line in the hvc0 log.
	// That line is emitted by the kernel immediately after poweroff() returns,
	// so it's the most reliable signal that all writes are flushed.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	const heartbeatEvery = 90 * time.Second
	lastHeartbeat := start

	for {
		select {
		case <-childDone:
			// Child exited before we saw the completion marker — either it
			// crashed during startup or the bake failed and rebooted, but we
			// missed the marker. Dump the hvc0 log so we can diagnose.
			hvc0Data, _ := os.ReadFile(hvc0Path)
			hvc0 := string(hvc0Data)
			if len(hvc0) > 0 {
				lines := strings.Split(hvc0, "\n")
				if len(lines) > 100 {
					lines = lines[len(lines)-100:]
				}
				log.Printf("[libkrun] bake VM: child exited early (%v elapsed) — serial log tail:\n%s",
					time.Since(start).Round(time.Second), strings.Join(lines, "\n"))
			} else {
				log.Printf("[libkrun] bake VM: child exited early (%v elapsed) — hvc0 log is empty",
					time.Since(start).Round(time.Second))
			}
			if childCmd.ProcessState != nil {
				return "", fmt.Errorf("bake VM exited early with code %d (%v elapsed) — see serial log above",
					childCmd.ProcessState.ExitCode(), time.Since(start).Round(time.Second))
			}
			return "", fmt.Errorf("bake VM exited early (%v elapsed) — see serial log above",
				time.Since(start).Round(time.Second))

		case <-ticker.C:
			elapsed := time.Since(start)

			// Emit a progress line every 90 s.
			if time.Since(lastHeartbeat) >= heartbeatEvery {
				log.Printf("[libkrun] bake VM: still running (%v elapsed) — check %s",
					elapsed.Round(time.Second), hvc0Path)
				lastHeartbeat = time.Now()
			}

			// Check hvc0 log for the kernel halt line.
			data, _ := os.ReadFile(hvc0Path)
			hvc0 := string(data)
			if strings.Contains(hvc0, "reboot: System halted") ||
				strings.Contains(hvc0, "agent bake: all tools installed") {
				log.Printf("[libkrun] bake VM: guest halted (%v elapsed) — waiting for libkrun to flush block device",
					elapsed.Round(time.Second))
				// Do NOT kill immediately. libkrun flushes its virtio-blk
				// write buffers asynchronously after the guest poweroff; killing
				// too early loses any in-flight writes (e.g. npm symlinks).
				// Wait up to 90 s for the process to exit on its own, then
				// give up and force-kill.
				exitWait := time.NewTimer(90 * time.Second)
				defer exitWait.Stop()
				exitPoll := time.NewTicker(500 * time.Millisecond)
				defer exitPoll.Stop()
			flushWait:
				for {
					select {
					case <-childDone:
						log.Printf("[libkrun] bake VM: process exited cleanly — rootfs flushed")
						break flushWait
					case <-exitWait.C:
						log.Printf("[libkrun] bake VM: process did not exit in 90 s — force-killing")
						_ = childCmd.Process.Kill()
						if passtProc != nil {
							_ = passtProc.Kill()
						}
						break flushWait
					case <-exitPoll.C:
						// still waiting
					}
				}
				goto verify
			}

			if elapsed > bakeTimeout {
				_ = childCmd.Process.Kill()
				if passtProc != nil {
					_ = passtProc.Kill()
				}
				// Dump the hvc0 log so we can see what the guest agent was doing
				// when the timeout hit (including per-step timing diagnostics).
				if hvc0Data, _ := os.ReadFile(hvc0Path); len(hvc0Data) > 0 {
					lines := strings.Split(string(hvc0Data), "\n")
					if len(lines) > 100 {
						lines = lines[len(lines)-100:]
					}
					log.Printf("[libkrun] bake VM: timed out after %v — serial log tail:\n%s",
						bakeTimeout, strings.Join(lines, "\n"))
				} else {
					log.Printf("[libkrun] bake VM: timed out after %v — hvc0 log is empty",
						bakeTimeout)
				}
				return "", fmt.Errorf("bake VM timed out after %v", bakeTimeout)
			}

		case <-ctx.Done():
			_ = childCmd.Process.Kill()
			if passtProc != nil {
				_ = passtProc.Kill()
			}
			return "", ctx.Err()
		}
	}

verify:

	// Run e2fsck to repair any ext4 journal state left by the bake VM. Force-
	// killing libkrun-vm can leave the filesystem in a dirty state (uncommitted
	// journal, stale block bitmaps). Repairing here ensures the baked rootfs is
	// clean before we verify the stamp and promote it to the base image.
	// e2fsck exits non-zero when it repairs, so we ignore the exit code.
	log.Printf("[libkrun] bake VM: running e2fsck on baked rootfs to repair any dirty journal state...")
	_ = exec.Command("e2fsck", "-f", "-y", rootfsPath).Run()

	// Read the hvc0 serial log to determine whether the bake succeeded.
	// We use the log as ground truth rather than the stamp file inside the
	// rootfs, because libkrun's virtio-blk write buffers can lose the stamp
	// write when the process is force-killed before it flushes to disk.
	hvc0Data, _ := os.ReadFile(hvc0Path)
	hvc0 := string(hvc0Data)

	// Only consider bake successful if the agent explicitly reported success.
	// Do NOT match "agent cli tools: all tools present" — that message can
	// appear when npm is missing (base packages failed) and the CLI tool
	// install is skipped, creating a false positive.
	bakeSucceeded := strings.Contains(hvc0, "agent bake: all tools installed")

	if !bakeSucceeded {
		// Log the tail for debugging, then fail.
		lines := strings.Split(hvc0, "\n")
		if len(lines) > 100 {
			lines = lines[len(lines)-100:]
		}
		log.Printf("[libkrun] bake VM: serial log tail:\n%s", strings.Join(lines, "\n"))

		// Check for common failure patterns to provide a better error message.
		var failureReason string
		if strings.Contains(hvc0, "Temporary failure resolving") {
			failureReason = "DNS resolution failed — the bake VM could not reach Ubuntu package repositories. Check host internet connectivity and passt networking configuration."
		} else if strings.Contains(hvc0, "Could not resolve") {
			failureReason = "DNS resolution failed — the bake VM could not reach Ubuntu package repositories. Check host internet connectivity and passt networking configuration."
		} else if strings.Contains(hvc0, "agent bake: FAILED") {
			failureReason = "package installation failed inside the bake VM — see serial log above for details"
		} else if strings.Contains(hvc0, "reboot: System halted") {
			failureReason = "VM powered off without reporting success — package installation may have failed silently"
		} else {
			failureReason = "tools-installed marker not found in serial log — package installation may have failed or timed out"
		}
		return "", fmt.Errorf("bake VM: %s", failureReason)
	}

	// The agent confirmed all tools are installed. Write the in-image toolchain
	// stamp directly into the rootfs using debugfs so it survives even if the
	// libkrun write-back flushed incompletely before force-kill.
	if err := writeStampIntoRootfs(rootfsPath); err != nil {
		log.Printf("[libkrun] bake VM: warning: could not write stamp via debugfs: %v", err)
		// Non-fatal: the workspace VMs will install tools in-VM on first boot,
		// but subsequent boots will be fast once the stamp is in place.
	}

	// npm install -g creates bin symlinks inside the VM, but libkrun's
	// virtio-blk write-back sometimes loses small metadata writes (symlinks)
	// when the VMM process is force-killed before it flushes.  Repair any
	// missing symlinks directly in the ext4 image using debugfs.
	if err := ensureNPMBinSymlinksInRootfs(rootfsPath); err != nil {
		log.Printf("[libkrun] bake VM: warning: could not repair npm symlinks via debugfs: %v", err)
	}

	// Move the baked rootfs OUT of workDir before defer removes the workDir.
	// os.Rename works across locations in the same tmpfs.
	bakedTmp, err := os.CreateTemp("", "nexus-baked-rootfs-*.ext4")
	if err != nil {
		return "", fmt.Errorf("create baked rootfs temp: %w", err)
	}
	bakedTmpPath := bakedTmp.Name()
	_ = bakedTmp.Close()
	if err := os.Rename(rootfsPath, bakedTmpPath); err != nil {
		_ = os.Remove(bakedTmpPath)
		return "", fmt.Errorf("move baked rootfs out of workdir: %w", err)
	}
	// workDir/rootfs.ext4 is now gone; defer will clean up remaining files.
	return bakedTmpPath, nil
}

// writeStampIntoRootfs writes the in-image toolchain stamp directly into the ext4
// rootfs image using debugfs. This is called after the bake VM completes so
// that the stamp is reliably persisted even when libkrun's virtio-blk
// write-back didn't flush before the process was force-killed.
func writeStampIntoRootfs(rootfsPath string) error {
	// The agent checks for this stamp to skip package installation on subsequent boots.
	const stampInsideRootfs = "/var/lib/nexus-tools-base-v7"

	// Write a temporary host-side file with the stamp content, then inject it
	// into the ext4 image using debugfs. debugfs operates on the raw image
	// bytes, bypassing the VM entirely.
	stampFile, err := os.CreateTemp("", "nexus-bake-stamp-*")
	if err != nil {
		return fmt.Errorf("create stamp temp file: %w", err)
	}
	stampPath := stampFile.Name()
	defer os.Remove(stampPath)
	if _, err := stampFile.WriteString("ok\n"); err != nil {
		_ = stampFile.Close()
		return fmt.Errorf("write stamp content: %w", err)
	}
	_ = stampFile.Close()

	// Ensure the /var/lib directory exists inside the rootfs (it should for Ubuntu).
	// debugfs "mkdir" is idempotent-ish but errors if the directory already exists,
	// so we ignore the error.
	_ = exec.Command("debugfs", "-w", "-R", "mkdir /var/lib", rootfsPath).Run()

	// debugfs "write <host-path> <guest-path>" copies the host file into the image.
	out, err := exec.Command("debugfs", "-w", "-R",
		fmt.Sprintf("write %s %s", stampPath, stampInsideRootfs),
		rootfsPath,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("debugfs write stamp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("[libkrun] bake VM: wrote bake stamp %s into rootfs via debugfs", stampInsideRootfs)
	return nil
}

// ensureNPMBinSymlinksInRootfs creates the global bin symlinks for npm
// CLI packages directly inside the ext4 image using debugfs.  This repairs
// symlinks that libkrun's virtio-blk write-back may have lost when the VMM
// process was force-killed before flushing metadata.
func ensureNPMBinSymlinksInRootfs(rootfsPath string) error {
	symlinks := []struct {
		link   string
		target string
	}{
		{"/usr/local/bin/opencode", "/usr/local/lib/node_modules/opencode-ai/bin/opencode"},
		{"/usr/local/bin/codex", "/usr/local/lib/node_modules/@openai/codex/bin/codex.js"},
		{"/usr/local/bin/claude", "/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe"},
	}

	for _, sl := range symlinks {
		// Check if the symlink already exists.
		out, _ := exec.Command("debugfs", "-R", "stat "+sl.link, rootfsPath).CombinedOutput()
		if strings.Contains(string(out), "Type: symlink") {
			continue
		}
		// Ensure parent directory exists.
		_ = exec.Command("debugfs", "-w", "-R", "mkdir /usr/local", rootfsPath).Run()
		_ = exec.Command("debugfs", "-w", "-R", "mkdir /usr/local/bin", rootfsPath).Run()
		out, err := exec.Command("debugfs", "-w", "-R",
			fmt.Sprintf("symlink %s %s", sl.link, sl.target),
			rootfsPath,
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("debugfs symlink %s -> %s: %w: %s", sl.link, sl.target, err, strings.TrimSpace(string(out)))
		}
		log.Printf("[libkrun] bake VM: created npm symlink %s -> %s via debugfs", sl.link, sl.target)
	}
	return nil
}

// createBakeWorkspaceImage creates a minimal empty ext4 image for the bake
// VM's /workspace mount. The bake agent doesn't use /workspace but the VM
// spec requires a block device for /dev/vdb.
func createBakeWorkspaceImage(path string) error {
	const bakeworkspaceSize = 512 * 1024 * 1024 // 512 MiB sparse
	if err := createSparseFile(path, bakeworkspaceSize); err != nil {
		return err
	}
	out, err := exec.Command("mkfs.ext4", "-F", path).CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("mkfs.ext4 bake workspace: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
