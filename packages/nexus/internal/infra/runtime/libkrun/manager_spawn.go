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

// Spawn creates and starts a new libkrun VM.
//
// Disk layout inside the guest (assembled by the agent):
//
//	/dev/vda  rootfs.ext4             (reflink clone, rw)     → /  (block rootfs)
//	/dev/vdb  workspace.ext4          (workspace clone, rw)   → /workspace
//	/dev/vdc  docker-data.ext4        (sparse, Docker data)   → /var/lib/docker
//	/dev/vdd  workspace-base.ext4     (read-only, optional)   → /workspace-base (fork/import only)
//	virtiofs "nexus-host-config"      (read-only, optional)   → /run/nexus-host (host config files)
//	virtiofs "nexus-workspace"        optional auxiliary host share
func (m *Manager) Spawn(ctx context.Context, spec SpawnSpec) (*Instance, error) {
	spawnStart := time.Now()
	log.Printf("[libkrun] Spawn start: workspace=%s", spec.WorkspaceID)
	defer func() {
		log.Printf("[libkrun] Spawn done: workspace=%s (%s)", spec.WorkspaceID, time.Since(spawnStart).Round(time.Millisecond))
	}()

	// Apply overall spawn timeout if the caller hasn't set one.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spawnTimeout)
		defer cancel()
	}

	m.mu.Lock()
	if _, exists := m.instances[spec.WorkspaceID]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("workspace already exists: %s", spec.WorkspaceID)
	}
	m.mu.Unlock()

	workDir := filepath.Join(m.cfg.WorkDirRoot, spec.WorkspaceID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("create workdir: %w", err)
	}
	// Ensure stale runtime artifacts from crashed/old daemons are cleaned before
	// starting a new VM instance in this workdir.
	cleanupWorkspaceRuntimeArtifacts(workDir, spec.WorkspaceID)

	serialLog := filepath.Join(workDir, "libkrun.log")
	workspacePath := filepath.Join(workDir, "workspace.ext4")
	workspaceBasePath := filepath.Join(workDir, "workspace-base.ext4")
	dockerDataPath := filepath.Join(workDir, "docker-data.ext4")
	rootfsPath := filepath.Join(workDir, "rootfs.ext4")

	// Workspace upperdir: restore from snapshot or create empty.
	if snapID := strings.TrimSpace(spec.SnapshotID); snapID != "" {
		if spec.ForkRestore {
			// Fork/restore: the snapshot becomes the base lowerdir.
			// Copy it to workspace-base.ext4 and create a fresh empty upperdir.
			log.Printf("[libkrun] workspace %s: phase=restore_base snapshot=%s", spec.WorkspaceID, snapID)
			phaseStart := time.Now()
			phaseCtx, cancel := phaseTimeout(ctx, 2*time.Minute)
			if err := copyFileWithContext(phaseCtx, m.snapshotWorkspacePath(snapID), workspaceBasePath); err != nil {
				cancel()
				os.RemoveAll(workDir)
				return nil, fmt.Errorf("restore base from snapshot: %w", err)
			}
			cancel()
			log.Printf("[libkrun] workspace %s: phase_done=restore_base (%s)", spec.WorkspaceID, time.Since(phaseStart).Round(time.Millisecond))

			// Create fresh empty upperdir for child mutations.
			log.Printf("[libkrun] workspace %s: phase=create_fork_upperdir", spec.WorkspaceID)
			upperPhaseStart := time.Now()
			phaseCtx, cancel = phaseTimeout(ctx, 1*time.Minute)
			if err := createWorkspaceOverlayWithContext(phaseCtx, workspacePath); err != nil {
				cancel()
				os.RemoveAll(workDir)
				return nil, fmt.Errorf("create fork upperdir: %w", err)
			}
			cancel()
			log.Printf("[libkrun] workspace %s: phase_done=create_fork_upperdir (%s)", spec.WorkspaceID, time.Since(upperPhaseStart).Round(time.Millisecond))
		} else {
			log.Printf("[libkrun] workspace %s: phase=restore_workspace snapshot=%s", spec.WorkspaceID, snapID)
			phaseStart := time.Now()
			phaseCtx, cancel := phaseTimeout(ctx, 2*time.Minute)
			if err := copyFileWithContext(phaseCtx, m.snapshotWorkspacePath(snapID), workspacePath); err != nil {
				cancel()
				os.RemoveAll(workDir)
				return nil, fmt.Errorf("restore workspace from snapshot: %w", err)
			}
			cancel()
			log.Printf("[libkrun] workspace %s: phase_done=restore_workspace (%s)", spec.WorkspaceID, time.Since(phaseStart).Round(time.Millisecond))
			log.Printf("[libkrun] workspace %s: restored workspace from snapshot %s", spec.WorkspaceID, snapID)
		}
	} else if _, err := os.Stat(workspacePath); err != nil {
		log.Printf("[libkrun] workspace %s: workspace mount strategy=hybrid overlay (empty upperdir)", spec.WorkspaceID)
		log.Printf("[libkrun] workspace %s: phase=create_workspace_upperdir", spec.WorkspaceID)
		upperPhaseStart := time.Now()
		phaseCtx, cancel := phaseTimeout(ctx, 1*time.Minute)
		if err := createWorkspaceOverlayWithContext(phaseCtx, workspacePath); err != nil {
			cancel()
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("create workspace upperdir: %w", err)
		}
		cancel()
		log.Printf("[libkrun] workspace %s: phase_done=create_workspace_upperdir (%s)", spec.WorkspaceID, time.Since(upperPhaseStart).Round(time.Millisecond))
		log.Printf("[libkrun] workspace %s: created empty workspace upperdir", spec.WorkspaceID)
	}

	// Workspace base image (read-only lowerdir): only required for hybrid
	// overlay mode (export/import flows). Regular workspace starts skip this
	// to stay within the 16-IRQ limit imposed by libkrun.
	if spec.UseWorkspaceBase {
		if _, err := os.Stat(workspaceBasePath); err != nil {
			log.Printf("[libkrun] workspace %s: phase=ensure_base_image", spec.WorkspaceID)
			basePhaseStart := time.Now()
			phaseCtx, cancel := phaseTimeout(ctx, 3*time.Minute)
			basePath, err := EnsureBaseImage(phaseCtx, spec.ProjectRoot, m.cfg.BasesDir, spec.ManifestHash)
			cancel()
			if err != nil {
				os.RemoveAll(workDir)
				return nil, fmt.Errorf("ensure base workspace image: %w", err)
			}
			log.Printf("[libkrun] workspace %s: phase_done=ensure_base_image (%s)", spec.WorkspaceID, time.Since(basePhaseStart).Round(time.Millisecond))
			log.Printf("[libkrun] workspace %s: phase=clone_workspace_base", spec.WorkspaceID)
			clonePhaseStart := time.Now()
			phaseCtx, cancel = phaseTimeout(ctx, 2*time.Minute)
			if err := copyFileWithContext(phaseCtx, basePath, workspaceBasePath); err != nil {
				cancel()
				os.RemoveAll(workDir)
				return nil, fmt.Errorf("clone workspace base image: %w", err)
			}
			cancel()
			log.Printf("[libkrun] workspace %s: phase_done=clone_workspace_base (%s)", spec.WorkspaceID, time.Since(clonePhaseStart).Round(time.Millisecond))
			log.Printf("[libkrun] workspace %s: cloned workspace base image", spec.WorkspaceID)
		}
	}

	// Docker data image: restore from snapshot for forks, otherwise create fresh.
	if snapID := strings.TrimSpace(spec.SnapshotID); snapID != "" {
		log.Printf("[libkrun] workspace %s: phase=restore_docker_data snapshot=%s", spec.WorkspaceID, snapID)
		phaseStart := time.Now()
		phaseCtx, cancel := phaseTimeout(ctx, 2*time.Minute)
		if err := copyFileWithContext(phaseCtx, m.snapshotDockerPath(snapID), dockerDataPath); err != nil {
			cancel()
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("restore docker-data from snapshot: %w", err)
		}
		cancel()
		log.Printf("[libkrun] workspace %s: phase_done=restore_docker_data (%s)", spec.WorkspaceID, time.Since(phaseStart).Round(time.Millisecond))
		log.Printf("[libkrun] workspace %s: restored docker-data from snapshot %s", spec.WorkspaceID, snapID)
	} else if _, err := os.Stat(dockerDataPath); err != nil {
		log.Printf("[libkrun] workspace %s: phase=create_docker_data", spec.WorkspaceID)
		phaseStart := time.Now()
		phaseCtx, cancel := phaseTimeout(ctx, 1*time.Minute)
		if err := createDockerDataImageWithContext(phaseCtx, dockerDataPath); err != nil {
			cancel()
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("create docker-data image: %w", err)
		}
		cancel()
		log.Printf("[libkrun] workspace %s: phase_done=create_docker_data (%s)", spec.WorkspaceID, time.Since(phaseStart).Round(time.Millisecond))
		log.Printf("[libkrun] workspace %s: created docker-data image", spec.WorkspaceID)
	}

	// Rootfs image: reflink clone from the baked base rootfs so each workspace
	// gets its own writable block rootfs with true guest root ownership.
	// XFS reflink is required for O(1) CoW clones; validation happens at driver
	// init time.
	if _, err := os.Stat(rootfsPath); err != nil {
		log.Printf("[libkrun] workspace %s: phase=clone_rootfs", spec.WorkspaceID)
		phaseStart := time.Now()
		phaseCtx, cancel := phaseTimeout(ctx, 2*time.Minute)
		if err := copyFileWithContext(phaseCtx, m.cfg.RootFSBasePath, rootfsPath); err != nil {
			cancel()
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("clone rootfs image: %w", err)
		}
		cancel()
		log.Printf("[libkrun] workspace %s: phase_done=clone_rootfs (%s)", spec.WorkspaceID, time.Since(phaseStart).Round(time.Millisecond))
		log.Printf("[libkrun] workspace %s: cloned rootfs image", spec.WorkspaceID)
	}

	// fsck is intentionally skipped here. ext4 journal replay on mount
	// inside the VM is sufficient to recover from unclean stops — that is
	// the entire purpose of ext4 journaling. Running e2fsck -f before every
	// boot adds 5-15s to spawn time with no correctness benefit in the
	// common case. Actual filesystem corruption (hardware/driver bugs) will
	// surface as a boot failure, which is the right failure mode.

	// Pick a free host TCP port for SSH forwarding (TSI maps it to guest port 22).
	sshPort, err := pickFreePort()
	if err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("pick ssh port: %w", err)
	}

	// vsock port configurations.
	vsockPorts := []VsockPortCfg{
		{
			Port:   DefaultAgentVSockPort,
			Path:   filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", DefaultAgentVSockPort)),
			Listen: true, // libkrun creates socket; host dials to reach guest agent
		},
		{
			Port:   VendingVSockPort,
			Path:   sshAgentSockPath(workDir),
			Listen: false, // host listens; guest dials for SSH agent
		},
		{
			Port:   DefaultSpotlightVSockPort,
			Path:   filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", DefaultSpotlightVSockPort)),
			Listen: true, // libkrun creates socket; host dials for spotlight
		},
		{
			Port:   GitHookVSockPort,
			Path:   gitHookSockPath(workDir),
			Listen: false, // host listens; guest dials to report branch changes
		},
		{
			Port:   DockerCredHelperVSockPort,
			Path:   dockerCredHelperSockPath(workDir),
			Listen: false, // host listens; guest dials to proxy docker credential requests
		},
	}

	kernelCmdline := "console=hvc0 root=/dev/vda rw rootfstype=ext4 init=/usr/local/bin/nexus-guest-agent"
	if gw := strings.TrimSpace(hostDefaultGatewayIP()); gw != "" {
		kernelCmdline += " nexus.gw=" + gw
	}
	if dns := strings.Join(hostDNSServers(), ","); dns != "" {
		kernelCmdline += " nexus.dns=" + dns
	}
	if profile := strings.TrimSpace(spec.VMProfile); profile != "" {
		kernelCmdline += " nexus.profile=" + profile
	}
	if spec.BakedRootfs {
		kernelCmdline += " nexus.baked=1"
	}

	vmSpec := VMSpec{
		WorkspaceID:       spec.WorkspaceID,
		WorkspaceMode:     "virtiofs", // default: regular workspace uses writable virtiofs
		KernelPath:        m.cfg.KernelPath,
		KernelCmdline:     kernelCmdline,
		RootFSImage:       rootfsPath,
		AgentPath:         "/usr/local/bin/nexus-guest-agent",
		BakedRootfs:       spec.BakedRootfs,
		WorkspaceImage:    workspacePath,
		WorkspaceHostPath: strings.TrimSpace(spec.ProjectRoot),
		DockerDataImage:   dockerDataPath,
		HostConfigDir:     spec.HostConfigDir,
		MemoryMiB:         spec.MemoryMiB,
		VCPUs:             spec.VCPUs,
		SerialLog:         serialLog,
		SSHHostPort:       sshPort,
		NetworkBackend:    strings.TrimSpace(m.cfg.NetworkBackend),
		PortMap:           []string{fmt.Sprintf("%d:22", sshPort)},
		VsockPorts:        vsockPorts,
	}
	if spec.ForkRestore {
		vmSpec.WorkspaceMode = "hybrid"
	}
	if spec.UseWorkspaceBase {
		vmSpec.WorkspaceBaseImage = workspaceBasePath
	}
	if vmSpec.NetworkBackend == "" {
		vmSpec.NetworkBackend = "auto"
	}
	if vmSpec.NetworkBackend == "auto" {
		// Prefer virtio-net when available (libkrun exports krun_add_net_unixstream);
		// fall back to TSI otherwise. virtio-net avoids TSI-specific issues with
		// seccomp/sandbox in applications like sshd.
		vmSpec.NetworkBackend = "virtio-net"
		log.Printf("[libkrun] workspace %s: network backend auto->virtio-net", spec.WorkspaceID)
	}

	var vmSideFD, hostSideFD *os.File
	var passtProc *os.Process
	if vmSpec.NetworkBackend == "auto" || vmSpec.NetworkBackend == "virtio-net" {
		log.Printf("[libkrun] workspace %s: phase=start_passt", spec.WorkspaceID)
		phaseStart := time.Now()
		var pairErr error
		vmSideFD, hostSideFD, pairErr = createPasstSocketPair()
		if pairErr != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("create passt socketpair: %w", pairErr)
		}
		// passt must live for the entire VM lifetime. Do not bind it to the
		// request-scoped start context, because that context is canceled when the
		// RPC handler returns and would SIGKILL passt, leaving guest networking
		// in link-local fallback only.
		passtProc, pairErr = startPasstProcess(context.Background(), workDir, hostSideFD, sshPort, passtGuestIPv4ForWorkspace(spec.WorkspaceID))
		if pairErr != nil {
			_ = vmSideFD.Close()
			_ = hostSideFD.Close()
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("start passt: %w", pairErr)
		}
		vmSpec.PasstFDIndex = 0
		log.Printf("[libkrun] workspace %s: passt started", spec.WorkspaceID)
		log.Printf("[libkrun] workspace %s: phase_done=start_passt (%s)", spec.WorkspaceID, time.Since(phaseStart).Round(time.Millisecond))
	}

	// Write VMSpec to a temp config file.
	configPath := filepath.Join(workDir, "libkrun-config.json")
	configData, err := json.Marshal(vmSpec)
	if err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("marshal vm spec: %w", err)
	}
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("write vm config: %w", err)
	}

	// Spawn the libkrun-vm child process. Use the extracted standalone binary
	// when available (packaged builds); fall back to the main nexus binary with
	// the "libkrun-vm" subcommand for smolvm/dev builds.
	vmBin := strings.TrimSpace(m.cfg.LibkrunVMBin)
	var libDir string
	var childCmd *exec.Cmd
	if vmBin != "" {
		libDir = filepath.Join(filepath.Dir(vmBin), "..", "lib")
		childCmd = exec.Command(vmBin, "--config="+configPath)
	} else if nexusBin := strings.TrimSpace(m.cfg.NexusBin); nexusBin != "" {
		libDir = m.cfg.LibkrunLibDir
		childCmd = exec.Command(nexusBin, "libkrun-vm", "--config="+configPath)
	} else {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("LibkrunVMBin not configured: run 'nexus daemon start --driver=libkrun' to bootstrap")
	}
	env := append(os.Environ(), "LD_LIBRARY_PATH="+libDir+":"+os.Getenv("LD_LIBRARY_PATH"))
	childCmd.Env = env
	childCmd.Dir = workDir
	if vmSideFD != nil {
		childCmd.ExtraFiles = []*os.File{vmSideFD}
	}

	// Redirect child stdout/stderr to the VM's serial log.
	vmLogFile, err := os.OpenFile(serialLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		vmLogFile = os.Stderr
	}
	childCmd.Stdout = vmLogFile
	childCmd.Stderr = vmLogFile

	log.Printf("[libkrun] workspace %s: phase=start_vm", spec.WorkspaceID)
	startVMPhase := time.Now()
	if err := childCmd.Start(); err != nil {
		if passtProc != nil {
			_ = passtProc.Kill()
		}
		if vmSideFD != nil {
			_ = vmSideFD.Close()
		}
		if hostSideFD != nil {
			_ = hostSideFD.Close()
		}
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("spawn libkrun-vm child: %w", err)
	}
	log.Printf("[libkrun] workspace %s: vm started (pid=%d)", spec.WorkspaceID, childCmd.Process.Pid)
	log.Printf("[libkrun] workspace %s: phase_done=start_vm (%s)", spec.WorkspaceID, time.Since(startVMPhase).Round(time.Millisecond))
	if vmSideFD != nil {
		_ = vmSideFD.Close()
	}
	if hostSideFD != nil {
		_ = hostSideFD.Close()
	}
	_ = vmLogFile.Close()

	// Write PID file.
	_ = os.WriteFile(filepath.Join(workDir, libkrunPIDFileName), []byte(strconv.Itoa(childCmd.Process.Pid)), 0o600)
	if passtProc != nil {
		_ = os.WriteFile(filepath.Join(workDir, passtPIDFileName), []byte(strconv.Itoa(passtProc.Pid)), 0o600)
	}
	// Mark the VM as "dirty": disk images may have unflushed kernel page-cache
	// writes. The flag is removed only on clean Stop(). If the daemon crashes or
	// the VM is killed, the flag remains, signalling to CheckpointFork /
	// ForkWorkspaceImage that it must fsync before copying.
	_ = os.WriteFile(filepath.Join(workDir, vmDirtyFlagFileName), []byte(strconv.Itoa(childCmd.Process.Pid)), 0o600)

	inst := &Instance{
		WorkspaceID:     spec.WorkspaceID,
		WorkDir:         workDir,
		WorkspaceImage:  workspacePath,
		DockerDataImage: dockerDataPath,
		RootfsImage:     rootfsPath,
		SerialLog:       serialLog,
		PasstProcess:    passtProc,
		GuestIP:         fmt.Sprintf("127.0.0.1:%d", sshPort),
		Process:         childCmd.Process,
		SSHPort:         sshPort,
	}

	m.mu.Lock()
	m.instances[spec.WorkspaceID] = inst
	m.mu.Unlock()

	// Start SSH agent proxy (listens on the vsock_10790.sock the guest will dial).
	startSSHAgentProxy(sshAgentSockPath(workDir))

	// Start Docker credential helper proxy (listens on vsock_10793.sock the guest will dial).
	startDockerCredHelperProxy(dockerCredHelperSockPath(workDir))

	log.Printf("[libkrun] started workspace %s (pid=%d ssh=127.0.0.1:%d)",
		spec.WorkspaceID, childCmd.Process.Pid, sshPort)

	// Reap passt in the background to prevent zombie accumulation when it exits.
	if passtProc != nil {
		go func(wsID string, p *os.Process, wd string) {
			if _, err := p.Wait(); err != nil {
				log.Printf("[libkrun] workspace %s: passt exited: %v", wsID, err)
			} else {
				log.Printf("[libkrun] workspace %s: passt exited", wsID)
			}
			// Dump passt log on unexpected exit to aid diagnostics.
			if logData, err := os.ReadFile(filepath.Join(wd, "passt.log")); err == nil && len(logData) > 0 {
				log.Printf("[libkrun] workspace %s: passt log:\n%s", wsID, string(logData))
			}
		}(spec.WorkspaceID, passtProc, workDir)
	}

	// Reap the VM/passt processes in the background so unexpected exits are
	// visible in daemon logs and stale "running" instances don't linger forever.
	go m.monitorInstance(spec.WorkspaceID, inst)

	return inst, nil
}
