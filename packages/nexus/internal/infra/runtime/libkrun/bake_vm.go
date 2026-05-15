//go:build linux

package libkrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cleanupStaleBakeArtifacts kills any orphan passt processes and removes stale
// bake temp dirs left over from a previous failed bake run. This is called
// before each bake attempt so a daemon restart after a failed bake always
// starts clean.
func cleanupStaleBakeArtifacts() {
	// Kill orphan passt processes that were started by a previous bake VM.
	// Pattern matches the args nexus always passes: "passt --fd <n> --foreground".
	out, err := exec.Command("pgrep", "-f", "passt --fd").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			pid, err := strconv.Atoi(line)
			if err != nil || pid <= 0 {
				continue
			}
			proc, err := os.FindProcess(pid)
			if err == nil {
				if killErr := proc.Kill(); killErr == nil {
					log.Printf("[libkrun] bake cleanup: killed orphan passt pid=%d", pid)
				}
			}
		}
	}

	// Remove stale bake temp dirs. Each runBakeVM call creates a
	// /tmp/nexus-rootfs-bake-* dir and defers its removal. If the process was
	// killed before the defer ran, the dir (and any large rootfs clone inside)
	// is left on disk. Clean them up before we clone again.
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "nexus-rootfs-bake-*"))
	for _, dir := range matches {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("[libkrun] bake cleanup: remove stale bake dir %s: %v", dir, err)
		} else {
			log.Printf("[libkrun] bake cleanup: removed stale bake dir %s", dir)
		}
	}
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

	bakeGW := strings.TrimSpace(hostDefaultGatewayIP())
	if bakeGW == "" {
		bakeGW = "192.168.0.1"
	}

	log.Printf("[libkrun] bake VM: starting passt for outbound internet access")
	vmSideFD, hostSideFD, err := createPasstSocketPair()
	if err != nil {
		return "", fmt.Errorf("create passt socketpair: %w", err)
	}
	// NOTE: Do NOT use a timeout context for passt — it is a daemon that runs
	// for the lifetime of the VM. Using a timeout would kill it prematurely.
	passtProc, err := startPasstProcess(ctx, workDir, hostSideFD, sshPort, passtGuestIPv4ForWorkspaceWithGateway("rootfs-bake", bakeGW))
	if err != nil {
		_ = vmSideFD.Close()
		_ = hostSideFD.Close()
		return "", fmt.Errorf("start passt: %w", err)
	}
	// NOTE: Do NOT close hostSideFD here — passt owns it via ExtraFiles.
	// It will be closed after childCmd starts (same pattern as manager.go).

	kernelCmdline := "console=hvc0 root=/dev/vda rw init=/usr/local/bin/nexus-guest-agent nexus.bake=1 nexus.gw=" + bakeGW
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

	// Pre-flight diagnostics: verify the binary exists and can execute.
	if fi, err := os.Stat(cfg.LibkrunVMBin); err != nil {
		log.Printf("[libkrun] bake VM: binary not found at %s: %v", cfg.LibkrunVMBin, err)
	} else {
		log.Printf("[libkrun] bake VM: binary=%s size=%d mod=%v", cfg.LibkrunVMBin, fi.Size(), fi.ModTime().Format(time.RFC3339))
	}
	preflight := exec.CommandContext(ctx, cfg.LibkrunVMBin, "--help")
	preflight.Env = env
	preflight.Dir = workDir
	if out, err := preflight.CombinedOutput(); err != nil {
		log.Printf("[libkrun] bake VM: preflight --help failed: %v output:\n%s", err, string(out))
	} else {
		log.Printf("[libkrun] bake VM: preflight --help ok (%d bytes)", len(out))
	}
	log.Printf("[libkrun] bake VM: config=%s", specPath)

	childCmd := exec.CommandContext(ctx, cfg.LibkrunVMBin, "--config="+specPath)
	childCmd.Env = env
	childCmd.Dir = workDir
	childCmd.ExtraFiles = []*os.File{vmSideFD}

	// Capture stderr synchronously via a pipe so we don't lose output when the
	// child exits quickly (fast-exit races with file-buffer flushes).
	stderrPipe, err := childCmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}
	childCmd.Stdout = os.Stdout // libkrun debug spam goes to parent stdout

	log.Printf("[libkrun] bake VM: starting — guest bootstrap in progress")

	if err := childCmd.Start(); err != nil {
		if passtProc != nil {
			_ = passtProc.Kill()
		}
		_ = vmSideFD.Close()
		_ = stderrPipe.Close()
		return "", fmt.Errorf("start bake VM: %w", err)
	}
	_ = vmSideFD.Close()
	_ = hostSideFD.Close()

	// Drain stderr in background so the pipe doesn't block the child.
	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stderrBuf, stderrPipe)
		_ = stderrPipe.Close()
		close(stderrDone)
	}()

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
	passtLogPath := filepath.Join(workDir, "passt.log")
	start := time.Now()

	// Poll every 5 s for the agent's "System halted" line in the hvc0 log.
	// That line is emitted by the kernel immediately after poweroff() returns,
	// so it's the most reliable signal that all writes are flushed.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	const heartbeatEvery = 90 * time.Second
	lastHeartbeat := start

	for {
		select {
		case <-childDone:
			// Wait for stderr drain to finish before reading the buffer.
			<-stderrDone
			// Child exited before we saw the completion marker — either it
			// crashed during startup or the bake failed and rebooted, but we
			// missed the marker. Dump both the launcher stderr and the hvc0
			// serial log so we can diagnose.
			stderrStr := stderrBuf.String()
			if len(stderrStr) > 0 {
				lines := strings.Split(stderrStr, "\n")
				// Log the first 20 lines (error message / signal info) and last 50
				// lines (stack trace) so we don't miss the root cause in truncation.
				headLines := lines
				if len(headLines) > 20 {
					headLines = headLines[:20]
				}
				tailLines := lines
				if len(tailLines) > 50 {
					tailLines = tailLines[len(tailLines)-50:]
				}
				log.Printf("[libkrun] bake VM: child exited early (%v elapsed) — launcher stderr head:\n%s",
					time.Since(start).Round(time.Second), strings.Join(headLines, "\n"))
				log.Printf("[libkrun] bake VM: child exited early (%v elapsed) — launcher stderr tail:\n%s",
					time.Since(start).Round(time.Second), strings.Join(tailLines, "\n"))
			} else {
				log.Printf("[libkrun] bake VM: child exited early (%v elapsed) — launcher stderr is empty",
					time.Since(start).Round(time.Second))
			}
			hvc0Data, _ := os.ReadFile(hvc0Path)
			if len(hvc0Data) > 0 {
				lines := strings.Split(string(hvc0Data), "\n")
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
				return "", fmt.Errorf("bake VM exited early with code %d (%v elapsed)",
					childCmd.ProcessState.ExitCode(), time.Since(start).Round(time.Second))
			}
			return "", fmt.Errorf("bake VM exited early (%v elapsed)",
				time.Since(start).Round(time.Second))

		case <-ticker.C:
			elapsed := time.Since(start)

			if pdata, _ := os.ReadFile(passtLogPath); len(pdata) > 0 {
				pl := strings.ToLower(string(pdata))
				if strings.Contains(pl, "invalid address") {
					_ = childCmd.Process.Kill()
					if passtProc != nil {
						_ = passtProc.Kill()
					}
					log.Printf("[libkrun] bake VM: passt rejected guest IP — log follows:\n%s", strings.TrimSpace(string(pdata)))
					return "", fmt.Errorf("passt startup failed (invalid --address); check passt.log under bake workdir")
				}
			}

			// Emit a progress line every 90 s.
			if time.Since(lastHeartbeat) >= heartbeatEvery {
				log.Printf("[libkrun] bake VM: still running (%v elapsed) — check %s",
					elapsed.Round(time.Second), hvc0Path)
				lastHeartbeat = time.Now()
			}

			// Check hvc0 log for explicit bake success marker.
			data, _ := os.ReadFile(hvc0Path)
			hvc0 := string(data)
			if strings.Contains(hvc0, "agent bake: FAILED") {
				_ = childCmd.Process.Kill()
				if passtProc != nil {
					_ = passtProc.Kill()
				}
				lines := strings.Split(hvc0, "\n")
				if len(lines) > 100 {
					lines = lines[len(lines)-100:]
				}
				log.Printf("[libkrun] bake VM: fail-fast marker observed (%v elapsed) — serial log tail:\n%s",
					elapsed.Round(time.Second), strings.Join(lines, "\n"))
				return "", fmt.Errorf("bake VM failed inside guest: agent reported failure")
			}
			if strings.Contains(hvc0, "agent bake: all tools installed") {
				log.Printf("[libkrun] bake VM: success marker observed (%v elapsed) — waiting for libkrun to flush block device",
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
