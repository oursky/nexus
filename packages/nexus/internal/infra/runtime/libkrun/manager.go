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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultAgentVSockPort is the vsock port for host→guest exec commands.
const DefaultAgentVSockPort uint32 = 10789

// DefaultSpotlightVSockPort is the vsock port for spotlight port-forwarding.
const DefaultSpotlightVSockPort uint32 = 10792

// ManagerConfig holds configuration for the libkrun VM manager.
type ManagerConfig struct {
	// LibkrunVMBin is the path to the nexus-libkrun-vm helper binary.
	// When non-empty (packaged builds with embedded artifacts), the manager
	// spawns this binary directly.  When empty it falls back to NexusBin with
	// the "libkrun-vm" subcommand (unpackaged / smolvm-fallback dev builds).
	LibkrunVMBin string
	// NexusBin is the path to the nexus binary (os.Executable()).
	// Fallback for dev builds that don't have LibkrunVMBin.
	NexusBin string
	// LibkrunLibDir is the directory containing libkrun.so and libkrunfw.so.
	// Used to set LD_LIBRARY_PATH when falling back to NexusBin; derived from
	// LibkrunVMBin path when using the standalone binary.
	LibkrunLibDir string

	// RootFSBasePath is the canonical rootfs.ext4 created during bootstrap.
	// It is kept for rootfs bake/bootstrap flows.
	RootFSBasePath string
	// KernelPath is the kernel image passed to krun_set_kernel.
	KernelPath string
	// BasesDir stores cached per-repo base workspace images.
	BasesDir string
	// NetworkBackend selects networking mode for libkrun VMs:
	// "auto" (prefer virtio-net), "virtio-net", or "tsi".
	NetworkBackend string

	WorkDirRoot string

	// EmbeddedAgentFn returns the embedded nexus-guest-agent binary bytes.
	// It is used by rootfs bake/bootstrap flows.
	EmbeddedAgentFn func() []byte
}

// Instance represents a running libkrun VM.
type Instance struct {
	WorkspaceID     string
	WorkDir         string
	WorkspaceImage  string // ext4 mounted at /workspace
	DockerDataImage string // sparse ext4 for /var/lib/docker
	SerialLog       string
	PasstProcess    *os.Process
	// GuestIP is the host-accessible SSH target (127.0.0.1:PORT).
	GuestIP string
	// Process is the libkrun-vm child process.
	Process *os.Process
	// SSHPort is the host TCP port that libkrun TSI maps to guest port 22.
	SSHPort int
}

// Manager handles libkrun VM lifecycle.
type Manager struct {
	cfg       ManagerConfig
	instances map[string]*Instance
	mu        sync.RWMutex
}

const (
	libkrunPIDFileName = "libkrun.pid"
	passtPIDFileName   = "passt.pid"
)

// NewManager creates a new Manager.
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.WorkDirRoot != "" {
		_ = os.MkdirAll(cfg.WorkDirRoot, 0o755)
	}
	return &Manager{
		cfg:       cfg,
		instances: make(map[string]*Instance),
	}
}

// SpawnSpec defines the configuration for spawning a new libkrun VM.
type SpawnSpec struct {
	WorkspaceID     string
	ProjectRoot     string
	SnapshotID      string // non-empty → restore overlay from this snapshot
	MemoryMiB       int
	VCPUs           int
	HostConfigDrive string
	VMProfile       string
	ManifestHash    string // derived from base+project Nexusfile contents
	BakedRootfs     bool   // true when the host rootfs bake stamp is present
}

// Spawn creates and starts a new libkrun VM.
//
// Disk layout inside the guest (assembled by the agent):
//
//	/dev/vda  rootfs.ext4             (reflink clone, rw)     → /  (block rootfs)
//	/dev/vdb  workspace.ext4          (workspace clone, rw)   → overlay upper for /workspace
//	/dev/vdc  docker-data.ext4        (sparse, Docker data)   → /var/lib/docker
//	/dev/vdd  hostconfig.ext4         (read-only, optional)   → /run/nexus-host
//	virtiofs "nexus-workspace"        project dir (ro lower layer)
//
// spawnTimeout is the maximum time allowed for the entire Spawn operation.
const spawnTimeout = 5 * time.Minute

// phaseTimeout returns a context with timeout for a specific spawn phase.
func phaseTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func signalProcess(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return 0, fmt.Errorf("invalid pid in %s", path)
	}
	return pid, nil
}

func processCmdline(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(data), "\x00", " ")
}

func isLikelyWorkspaceVMCmdline(cmdline, workspaceID string) bool {
	if strings.TrimSpace(cmdline) == "" {
		return false
	}
	if !strings.Contains(cmdline, workspaceID) {
		return false
	}
	return strings.Contains(cmdline, "nexus-libkrun-vm") || strings.Contains(cmdline, " libkrun-vm ")
}

func isLikelyPasstCmdline(cmdline string) bool {
	if strings.TrimSpace(cmdline) == "" {
		return false
	}
	return strings.Contains(cmdline, " passt ") || strings.HasPrefix(cmdline, "passt ")
}

func terminateProcessByPIDFile(pidFilePath string, verify func(cmdline string) bool) {
	pid, err := readPIDFile(pidFilePath)
	if err != nil {
		_ = os.Remove(pidFilePath)
		return
	}

	cmdline := processCmdline(pid)
	if verify != nil && !verify(cmdline) {
		_ = os.Remove(pidFilePath)
		return
	}

	// Best-effort graceful stop, then hard kill.
	_ = signalProcess(pid, syscall.SIGINT)
	time.Sleep(150 * time.Millisecond)
	if err := signalProcess(pid, syscall.Signal(0)); err == nil {
		_ = signalProcess(pid, syscall.SIGKILL)
	}
	_ = os.Remove(pidFilePath)
}

func cleanupWorkspaceRuntimeArtifacts(workDir, workspaceID string) {
	terminateProcessByPIDFile(
		filepath.Join(workDir, libkrunPIDFileName),
		func(cmdline string) bool { return isLikelyWorkspaceVMCmdline(cmdline, workspaceID) },
	)
	terminateProcessByPIDFile(
		filepath.Join(workDir, passtPIDFileName),
		isLikelyPasstCmdline,
	)
	for _, port := range []uint32{DefaultAgentVSockPort, DefaultSpotlightVSockPort} {
		_ = os.Remove(filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", port)))
	}
}

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
	dockerDataPath := filepath.Join(workDir, "docker-data.ext4")
	rootfsPath := filepath.Join(workDir, "rootfs.ext4")

	// Workspace image: restore from snapshot or clone repo base image.
	if snapID := strings.TrimSpace(spec.SnapshotID); snapID != "" {
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
	} else if _, err := os.Stat(workspacePath); err != nil {
		log.Printf("[libkrun] workspace %s: workspace mount strategy=virtiofs lower + ext4 overlay upper", spec.WorkspaceID)
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
		log.Printf("[libkrun] workspace %s: phase=clone_workspace", spec.WorkspaceID)
		clonePhaseStart := time.Now()
		phaseCtx, cancel = phaseTimeout(ctx, 2*time.Minute)
		if err := copyFileWithContext(phaseCtx, basePath, workspacePath); err != nil {
			cancel()
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("clone workspace image: %w", err)
		}
		cancel()
		log.Printf("[libkrun] workspace %s: phase_done=clone_workspace (%s)", spec.WorkspaceID, time.Since(clonePhaseStart).Round(time.Millisecond))
		log.Printf("[libkrun] workspace %s: cloned workspace image", spec.WorkspaceID)
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
	}

	kernelCmdline := "console=hvc0"
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
		WorkspaceMode:     "hybrid",
		KernelPath:        m.cfg.KernelPath,
		KernelCmdline:     kernelCmdline,
		RootFSImage:       rootfsPath,
		AgentPath:         "/usr/local/bin/nexus-guest-agent",
		BakedRootfs:       spec.BakedRootfs,
		WorkspaceImage:    workspacePath,
		WorkspaceHostPath: strings.TrimSpace(spec.ProjectRoot),
		DockerDataImage:   dockerDataPath,
		HostConfigDrive:   spec.HostConfigDrive,
		MemoryMiB:         spec.MemoryMiB,
		VCPUs:             spec.VCPUs,
		SerialLog:         serialLog,
		SSHHostPort:       sshPort,
		NetworkBackend:    strings.TrimSpace(m.cfg.NetworkBackend),
		PortMap:           []string{fmt.Sprintf("%d:22", sshPort)},
		VsockPorts:        vsockPorts,
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

	inst := &Instance{
		WorkspaceID:     spec.WorkspaceID,
		WorkDir:         workDir,
		WorkspaceImage:  workspacePath,
		DockerDataImage: dockerDataPath,
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

	log.Printf("[libkrun] started workspace %s (pid=%d ssh=127.0.0.1:%d)",
		spec.WorkspaceID, childCmd.Process.Pid, sshPort)

	// Reap passt in the background to prevent zombie accumulation when it exits.
	if passtProc != nil {
		go func(wsID string, p *os.Process) {
			if _, err := p.Wait(); err != nil {
				log.Printf("[libkrun] workspace %s: passt exited: %v", wsID, err)
			} else {
				log.Printf("[libkrun] workspace %s: passt exited", wsID)
			}
		}(spec.WorkspaceID, passtProc)
	}

	// Reap the VM/passt processes in the background so unexpected exits are
	// visible in daemon logs and stale "running" instances don't linger forever.
	go m.monitorInstance(spec.WorkspaceID, inst)

	return inst, nil
}

func (m *Manager) monitorInstance(workspaceID string, inst *Instance) {
	if inst == nil || inst.Process == nil {
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Signal 0 checks liveness without delivering a real signal.
		if err := inst.Process.Signal(syscall.Signal(0)); err != nil {
			log.Printf("[libkrun] workspace %s: vm process no longer alive: %v", workspaceID, err)
			if inst.PasstProcess != nil {
				if pErr := inst.PasstProcess.Signal(syscall.Signal(0)); pErr == nil {
					_ = inst.PasstProcess.Kill()
				}
			}
			_ = os.Remove(filepath.Join(inst.WorkDir, libkrunPIDFileName))
			_ = os.Remove(filepath.Join(inst.WorkDir, passtPIDFileName))

			m.mu.Lock()
			current, ok := m.instances[workspaceID]
			if ok && current == inst {
				delete(m.instances, workspaceID)
			}
			m.mu.Unlock()
			return
		}

		if inst.PasstProcess != nil {
			if pErr := inst.PasstProcess.Signal(syscall.Signal(0)); pErr != nil {
				log.Printf("[libkrun] workspace %s: passt process no longer alive: %v", workspaceID, pErr)
				// If passt dies, networking is irrecoverable for this VM (the shared
				// socketpair endpoint is gone). Kill the VM so callers get a clean
				// restart path instead of hanging on a half-dead instance.
				_ = inst.Process.Kill()
				_ = os.Remove(filepath.Join(inst.WorkDir, libkrunPIDFileName))
				_ = os.Remove(filepath.Join(inst.WorkDir, passtPIDFileName))

				m.mu.Lock()
				current, ok := m.instances[workspaceID]
				if ok && current == inst {
					delete(m.instances, workspaceID)
				}
				m.mu.Unlock()
				return
			}
		}
	}
}

// Stop terminates a running VM.
func (m *Manager) Stop(_ context.Context, workspaceID string) error {
	m.mu.Lock()
	inst, exists := m.instances[workspaceID]
	if exists {
		delete(m.instances, workspaceID)
	}
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("workspace not found: %s", workspaceID)
	}

	// Gracefully stop VM child process.
	if inst.Process != nil {
		if err := inst.Process.Signal(os.Interrupt); err != nil {
			_ = inst.Process.Kill()
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if err := inst.Process.Signal(syscall.Signal(0)); err != nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err := inst.Process.Signal(syscall.Signal(0)); err == nil {
			_ = inst.Process.Kill()
		}
	}
	if inst.PasstProcess != nil {
		if err := inst.PasstProcess.Signal(os.Interrupt); err != nil {
			_ = inst.PasstProcess.Kill()
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if err := inst.PasstProcess.Signal(syscall.Signal(0)); err != nil {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		if err := inst.PasstProcess.Signal(syscall.Signal(0)); err == nil {
			_ = inst.PasstProcess.Kill()
		}
	}
	_ = os.Remove(filepath.Join(inst.WorkDir, libkrunPIDFileName))
	_ = os.Remove(filepath.Join(inst.WorkDir, passtPIDFileName))
	return nil
}

// Get returns a running instance by workspace ID.
func (m *Manager) Get(workspaceID string) (*Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[workspaceID]
	if !ok {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}
	return inst, nil
}

// CleanupWorkspaceByID removes all state for a workspace (stops VM if running, removes workdir).
func (m *Manager) CleanupWorkspaceByID(ctx context.Context, workspaceID string) error {
	_ = m.Stop(ctx, workspaceID)
	workDir := filepath.Join(m.cfg.WorkDirRoot, workspaceID)
	return os.RemoveAll(workDir)
}

// snapshot image paths for fork/restore lineage.
func (m *Manager) snapshotWorkspacePath(snapshotID string) string {
	return filepath.Join(m.cfg.WorkDirRoot, ".snapshots", snapshotID+".workspace.ext4")
}

func (m *Manager) snapshotDockerPath(snapshotID string) string {
	return filepath.Join(m.cfg.WorkDirRoot, ".snapshots", snapshotID+".docker.ext4")
}

// ForkWorkspaceImage snapshots the parent's workspace and docker images for
// the child lineage snapshot.
// The caller (Driver.CheckpointFork) is responsible for stopping the parent VM
// before calling this and restarting it afterward.
// fallbackProjectRoot is the parent workspace's project root; it is used to
// materialise workspace images when the parent VM was never started.
func (m *Manager) ForkWorkspaceImage(_ context.Context, parentWorkspaceID, childSnapshotID, fallbackProjectRoot string) error {
	m.mu.RLock()
	parent, exists := m.instances[parentWorkspaceID]
	m.mu.RUnlock()

	// tmpDockerPath is set when we create a temporary docker image that should
	// be cleaned up after the snapshot copy finishes.
	var tmpDockerPath string

	var workspaceSrcPath, dockerSrcPath string
	if exists {
		workspaceSrcPath = parent.WorkspaceImage
		dockerSrcPath = parent.DockerDataImage
	} else {
		// Parent not currently running; find images from its workdir.
		parentDir := filepath.Join(m.cfg.WorkDirRoot, parentWorkspaceID)
		workspaceSrcPath = filepath.Join(parentDir, "workspace.ext4")
		dockerSrcPath = filepath.Join(parentDir, "docker-data.ext4")

		// When the workspace was registered but never booted the workdir
		// images don't exist yet. Fall back to base images so that a fork of
		// a freshly-created (unstarted) workspace still works.
		if _, err := os.Stat(workspaceSrcPath); err != nil {
			if fallbackProjectRoot == "" {
				return fmt.Errorf("parent workspace image not found at %s and no fallback project root: %w", workspaceSrcPath, err)
			}

			basePath, baseErr := EnsureBaseImage(context.Background(), fallbackProjectRoot, m.cfg.BasesDir, "")
			if baseErr != nil {
				return fmt.Errorf("ensure base workspace image for fork fallback: %w", baseErr)
			}
			workspaceSrcPath = basePath

			// Docker data image: create a fresh empty image into a temp file
			// so that the snapshot copy below works uniformly.
			tmpDockerPath = filepath.Join(m.cfg.WorkDirRoot, ".tmp-docker-"+childSnapshotID+".ext4")
			if createErr := createDockerDataImage(tmpDockerPath); createErr != nil {
				return fmt.Errorf("create docker-data image for fork fallback: %w", createErr)
			}
			dockerSrcPath = tmpDockerPath
		}
	}
	if tmpDockerPath != "" {
		defer os.Remove(tmpDockerPath)
	}

	for _, p := range []struct {
		label string
		path  string
	}{
		{label: "workspace", path: workspaceSrcPath},
		{label: "docker", path: dockerSrcPath},
	} {
		if _, err := os.Stat(p.path); err != nil {
			return fmt.Errorf("parent %s image not found at %s: %w", p.label, p.path, err)
		}
	}

	snapshotsDir := filepath.Join(m.cfg.WorkDirRoot, ".snapshots")
	if err := os.MkdirAll(snapshotsDir, 0o755); err != nil {
		return fmt.Errorf("create snapshots dir: %w", err)
	}

	copyItems := []struct {
		label string
		src   string
		dst   string
	}{
		{label: "workspace", src: workspaceSrcPath, dst: m.snapshotWorkspacePath(childSnapshotID)},
		{label: "docker", src: dockerSrcPath, dst: m.snapshotDockerPath(childSnapshotID)},
	}
	for _, item := range copyItems {
		if err := copyFile(item.src, item.dst); err != nil {
			return fmt.Errorf("snapshot %s image %s → %s: %w", item.label, item.src, item.dst, err)
		}
	}
	return nil
}

// ReconcileOrphans kills child processes whose workspace IDs are not in liveIDs.
func (m *Manager) ReconcileOrphans(_ context.Context, liveIDs map[string]struct{}) error {
	m.mu.Lock()
	var toStop []string
	for id := range m.instances {
		if _, ok := liveIDs[id]; !ok {
			toStop = append(toStop, id)
		}
	}
	m.mu.Unlock()

	for _, id := range toStop {
		log.Printf("[libkrun] reconcile: stopping orphan %s", id)
		_ = m.Stop(context.Background(), id)
	}

	entries, err := os.ReadDir(m.cfg.WorkDirRoot)
	if err != nil {
		return nil
	}
	workspaceDirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if !strings.HasPrefix(id, "ws-") {
			continue
		}
		if _, ok := liveIDs[id]; ok {
			continue
		}
		workspaceDirs = append(workspaceDirs, id)
	}
	sort.Strings(workspaceDirs)
	for _, id := range workspaceDirs {
		workDir := filepath.Join(m.cfg.WorkDirRoot, id)
		log.Printf("[libkrun] reconcile: cleaning runtime artifacts for %s", id)
		cleanupWorkspaceRuntimeArtifacts(workDir, id)
	}
	return nil
}

// GrowWorkspace resizes a workspace image. The guest agent handles the online resize.
func (m *Manager) GrowWorkspace(_ context.Context, workspaceID string, newSizeBytes int64) error {
	m.mu.RLock()
	inst, exists := m.instances[workspaceID]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("workspace not running: %s", workspaceID)
	}
	return resizeImage(inst.WorkspaceImage, newSizeBytes)
}
