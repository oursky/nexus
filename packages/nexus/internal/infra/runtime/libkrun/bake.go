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
	"strings"
	"time"
)

// bakeStampVersion is bumped whenever the set of tools baked into the
// base rootfs changes, forcing a re-bake on next daemon start.
const bakeStampVersion = "v4"

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
	stampPath := filepath.Join(stampDir, "rootfs-baked-"+bakeStampVersion)
	if _, err := os.Stat(stampPath); err == nil {
		log.Printf("[libkrun] rootfs bake: already baked (stamp %s)", stampPath)
		return nil
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

	log.Printf("[libkrun] rootfs bake: pre-installing developer tools into base rootfs (first run, ~5-10 min)...")

	bakedRootfsPath, err := runBakeVM(ctx, cfg)
	if err != nil {
		return fmt.Errorf("bake VM: %w", err)
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

// DeleteBakeStamp removes the host-side bake stamp so the next daemon start
// triggers a fresh bake. Called when the base rootfs is rebuilt from scratch.
func DeleteBakeStamp(stampDir string) {
	_ = os.Remove(filepath.Join(stampDir, "rootfs-baked-"+bakeStampVersion))
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
			if err := injectFileIntoExt4(rootfsPath, agentData, "/usr/local/bin/nexus-firecracker-agent", 0o755); err != nil {
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

	// The bake VM uses TSI networking (simpler than virtio-net+passt for an
	// ephemeral install-only VM). TSI doesn't support UDP DNS, but the bake
	// agent adds "nexus.tsi=1" awareness which forces "options use-vc" in
	// /etc/resolv.conf so all DNS queries use TCP (which TSI proxies).
	// SSH port forwarding is handled by TSI's port map via krun_set_port_map.
	sshPort, err := pickFreePort()
	if err != nil {
		return "", fmt.Errorf("pick free port: %w", err)
	}

	vmSpec := VMSpec{
		WorkspaceID: "rootfs-bake",
		KernelPath:  cfg.KernelPath,
		// nexus.tsi=1 tells the agent to write "options use-vc" in resolv.conf
		// so DNS queries use TCP and work through TSI's TCP proxy.
		KernelCmdline:   "console=hvc0 root=/dev/vda rw init=/usr/local/bin/nexus-firecracker-agent nexus.bake=1 nexus.tsi=1",
		RootFSImage:     rootfsPath,
		AgentPath:       "/usr/local/bin/nexus-firecracker-agent",
		WorkspaceImage:  workspacePath,
		DockerDataImage: dockerDataPath,
		MemoryMiB:       1024,
		VCPUs:           2,
		SerialLog:       serialLog,
		NetworkBackend:  "tsi",
		PortMap:         []string{fmt.Sprintf("%d:22", sshPort)},
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

	log.Printf("[libkrun] bake VM: starting — apt-get + npm install in progress")

	if err := childCmd.Start(); err != nil {
		if vmLogFile != nil {
			_ = vmLogFile.Close()
		}
		return "", fmt.Errorf("start bake VM: %w", err)
	}
	if vmLogFile != nil {
		_ = vmLogFile.Close()
	}

	// Reap the child in a goroutine. We don't block on it — libkrun's VMM
	// cleanup can hang for minutes after the guest poweroff. Instead we poll
	// the hvc0 serial log for the completion marker, then kill the process.
	go func() { _ = childCmd.Wait() }()

	const bakeTimeout = 20 * time.Minute
	hvc0Path := serialLog + ".hvc0"
	start := time.Now()

	// Poll every 5 s for the agent's "System halted" line in the hvc0 log.
	// That line is emitted by the kernel immediately after poweroff() returns,
	// so it's the most reliable signal that all writes are flushed.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	const heartbeatEvery = 30 * time.Second
	lastHeartbeat := start

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(start)

			// Emit a progress line every 30 s.
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
				// Wait up to 30 s for the process to exit on its own, then
				// give up and force-kill.
				exitWait := time.NewTimer(30 * time.Second)
				defer exitWait.Stop()
				exitPoll := time.NewTicker(500 * time.Millisecond)
				defer exitPoll.Stop()
				done2 := make(chan struct{})
				go func() {
					_ = childCmd.Wait()
					close(done2)
				}()
			flushWait:
				for {
					select {
					case <-done2:
						log.Printf("[libkrun] bake VM: process exited cleanly — rootfs flushed")
						break flushWait
					case <-exitWait.C:
						log.Printf("[libkrun] bake VM: process did not exit in 30 s — force-killing")
						_ = childCmd.Process.Kill()
						break flushWait
					case <-exitPoll.C:
						// still waiting
					}
				}
				goto verify
			}

			if elapsed > bakeTimeout {
				_ = childCmd.Process.Kill()
				return "", fmt.Errorf("bake VM timed out after %v — check %s", bakeTimeout, hvc0Path)
			}

		case <-ctx.Done():
			_ = childCmd.Process.Kill()
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

	bakeSucceeded := strings.Contains(hvc0, "agent bake: all tools installed") ||
		strings.Contains(hvc0, "agent cli tools: all tools present")

	if !bakeSucceeded {
		// Log the tail for debugging, then fail.
		lines := strings.Split(hvc0, "\n")
		if len(lines) > 50 {
			lines = lines[len(lines)-50:]
		}
		log.Printf("[libkrun] bake VM: serial log tail:\n%s", strings.Join(lines, "\n"))
		return "", fmt.Errorf("bake VM: tools-installed marker not found in serial log (check above for details)")
	}

	// The agent confirmed all tools are installed. Write the v4 stamp
	// directly into the rootfs using debugfs so it survives even if the
	// libkrun write-back flushed incompletely before force-kill.
	if err := writeStampIntoRootfs(rootfsPath); err != nil {
		log.Printf("[libkrun] bake VM: warning: could not write stamp via debugfs: %v", err)
		// Non-fatal: the workspace VMs will install tools in-VM on first boot,
		// but subsequent boots will be fast once the stamp is in place.
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

// writeStampIntoRootfs writes the v4 bake stamp file directly into the ext4
// rootfs image using debugfs. This is called after the bake VM completes so
// that the stamp is reliably persisted even when libkrun's virtio-blk
// write-back didn't flush before the process was force-killed.
func writeStampIntoRootfs(rootfsPath string) error {
	// The agent checks for this stamp to skip package installation on subsequent boots.
	const stampInsideRootfs = "/var/lib/nexus-tools-installed-v4"

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
