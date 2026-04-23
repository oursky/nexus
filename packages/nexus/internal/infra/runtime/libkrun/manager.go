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
	"sync"
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

	// RootFSBasePath is the canonical rootfs.ext4 created during bootstrap.
	// Each workspace clones it (reflink/sparse copy) into its own workdir so
	// the guest root is a real writable block filesystem.
	RootFSBasePath string
	// KernelPath is the kernel image passed to krun_set_kernel.
	KernelPath string
	// BasesDir stores cached per-repo base workspace images.
	BasesDir string
	// NetworkBackend selects networking mode for libkrun VMs:
	// "auto" (prefer virtio-net), "virtio-net", or "tsi".
	NetworkBackend string

	WorkDirRoot string

	// EmbeddedAgentFn returns the embedded nexus-firecracker-agent binary bytes.
	// When set, the agent binary is written into each workspace's rootfs.ext4
	// on spawn so the in-VM agent stays in sync with the nexus binary.
	EmbeddedAgentFn func() []byte
}

// Instance represents a running libkrun VM.
type Instance struct {
	WorkspaceID     string
	WorkDir         string
	RootFSImage     string
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
}

// Spawn creates and starts a new libkrun VM using block-backed root/workspace
// images plus a separate Docker data image.
//
// Disk layout inside the guest (assembled by the agent):
//
//	/dev/vda  rootfs.ext4             (workspace clone, rw)   → /
//	/dev/vdb  workspace.ext4          (workspace clone, rw)   → /workspace
//	/dev/vdc  docker-data.ext4        (sparse, Docker data)   → /var/lib/docker
//	/dev/vdd  hostconfig.ext4         (read-only)             → /run/nexus-host
func (m *Manager) Spawn(ctx context.Context, spec SpawnSpec) (*Instance, error) {
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

	// Remove stale vsock socket files from a previous run so libkrun can
	// recreate them. If they still exist, krun_add_vsock_port2 fails with
	// "file exists" and the agent never comes up.
	for _, port := range []uint32{DefaultAgentVSockPort, DefaultSpotlightVSockPort} {
		_ = os.Remove(filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", port)))
	}

	serialLog := filepath.Join(workDir, "libkrun.log")
	rootfsPath := filepath.Join(workDir, "rootfs.ext4")
	workspacePath := filepath.Join(workDir, "workspace.ext4")
	dockerDataPath := filepath.Join(workDir, "docker-data.ext4")

	// Rootfs: restore from snapshot for forks, otherwise clone from base rootfs.
	// clone uses reflink when available (XFS/btrfs) and sparse-copy fallback.
	if snapID := strings.TrimSpace(spec.SnapshotID); snapID != "" {
		if err := copyFile(m.snapshotRootfsPath(snapID), rootfsPath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("restore rootfs from snapshot: %w", err)
		}
		log.Printf("[libkrun] workspace %s: restored rootfs from snapshot %s", spec.WorkspaceID, snapID)
	} else if _, err := os.Stat(rootfsPath); err != nil {
		if err := copyFile(m.cfg.RootFSBasePath, rootfsPath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("clone rootfs image: %w", err)
		}
	}

	// Inject current embedded agent into the rootfs so the in-VM agent always
	// matches this nexus binary, regardless of when rootfs.ext4 was created.
	if m.cfg.EmbeddedAgentFn != nil {
		if agentData := m.cfg.EmbeddedAgentFn(); len(agentData) > 0 {
			if err := injectFileIntoExt4(rootfsPath, agentData, "/usr/local/bin/nexus-firecracker-agent", 0o755); err != nil {
				log.Printf("[libkrun] workspace %s: agent inject failed (non-fatal): %v", spec.WorkspaceID, err)
			}
		}
	}

	// Workspace image: restore from snapshot or clone repo base image.
	if snapID := strings.TrimSpace(spec.SnapshotID); snapID != "" {
		if err := copyFile(m.snapshotWorkspacePath(snapID), workspacePath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("restore workspace from snapshot: %w", err)
		}
		log.Printf("[libkrun] workspace %s: restored workspace from snapshot %s", spec.WorkspaceID, snapID)
	} else if _, err := os.Stat(workspacePath); err != nil {
		basePath, err := EnsureBaseImage(spec.ProjectRoot, m.cfg.BasesDir)
		if err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("ensure base workspace image: %w", err)
		}
		if err := copyFile(basePath, workspacePath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("clone workspace image: %w", err)
		}
	}

	// Docker data image: restore from snapshot for forks, otherwise create fresh.
	if snapID := strings.TrimSpace(spec.SnapshotID); snapID != "" {
		if err := copyFile(m.snapshotDockerPath(snapID), dockerDataPath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("restore docker-data from snapshot: %w", err)
		}
		log.Printf("[libkrun] workspace %s: restored docker-data from snapshot %s", spec.WorkspaceID, snapID)
	} else if _, err := os.Stat(dockerDataPath); err != nil {
		if err := createDockerDataImage(dockerDataPath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("create docker-data image: %w", err)
		}
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

	vmSpec := VMSpec{
		WorkspaceID:     spec.WorkspaceID,
		KernelPath:      m.cfg.KernelPath,
		KernelCmdline:   "console=hvc0 root=/dev/vda rw init=/usr/local/bin/nexus-firecracker-agent",
		RootFSImage:     rootfsPath,
		AgentPath:       "/usr/local/bin/nexus-firecracker-agent",
		WorkspaceImage:  workspacePath,
		DockerDataImage: dockerDataPath,
		HostConfigDrive: spec.HostConfigDrive,
		MemoryMiB:       spec.MemoryMiB,
		VCPUs:           spec.VCPUs,
		SerialLog:       serialLog,
		SSHHostPort:     sshPort,
		NetworkBackend:  strings.TrimSpace(m.cfg.NetworkBackend),
		PortMap:         []string{fmt.Sprintf("%d:22", sshPort)},
		VsockPorts:      vsockPorts,
	}
	if vmSpec.NetworkBackend == "" {
		vmSpec.NetworkBackend = "auto"
	}

	var vmSideFD, hostSideFD *os.File
	var passtProc *os.Process
	if vmSpec.NetworkBackend == "auto" || vmSpec.NetworkBackend == "virtio-net" {
		var pairErr error
		vmSideFD, hostSideFD, pairErr = createPasstSocketPair()
		if pairErr != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("create passt socketpair: %w", pairErr)
		}
		passtProc, pairErr = startPasstProcess(workDir, hostSideFD)
		if pairErr != nil {
			_ = vmSideFD.Close()
			_ = hostSideFD.Close()
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("start passt: %w", pairErr)
		}
		vmSpec.PasstFDIndex = 0
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

	// Spawn the libkrun-vm child process using the extracted standalone binary.
	vmBin := strings.TrimSpace(m.cfg.LibkrunVMBin)
	if vmBin == "" {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("LibkrunVMBin not configured: run 'nexus daemon start --driver=libkrun' to bootstrap")
	}
	libDir := filepath.Join(filepath.Dir(vmBin), "..", "lib")
	env := append(os.Environ(), "LD_LIBRARY_PATH="+libDir+":"+os.Getenv("LD_LIBRARY_PATH"))
	childCmd := exec.Command(vmBin, "--config="+configPath)
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
	if vmSideFD != nil {
		_ = vmSideFD.Close()
	}
	if hostSideFD != nil {
		_ = hostSideFD.Close()
	}
	_ = vmLogFile.Close()

	// Write PID file.
	_ = os.WriteFile(filepath.Join(workDir, "libkrun.pid"), []byte(strconv.Itoa(childCmd.Process.Pid)), 0o600)

	inst := &Instance{
		WorkspaceID:     spec.WorkspaceID,
		WorkDir:         workDir,
		RootFSImage:     rootfsPath,
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

	return inst, nil
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
		done := make(chan struct{})
		go func() {
			_, _ = inst.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			_ = inst.Process.Kill()
		}
	}
	if inst.PasstProcess != nil {
		if err := inst.PasstProcess.Signal(os.Interrupt); err != nil {
			_ = inst.PasstProcess.Kill()
		}
		done := make(chan struct{})
		go func() {
			_, _ = inst.PasstProcess.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = inst.PasstProcess.Kill()
		}
	}
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
func (m *Manager) snapshotRootfsPath(snapshotID string) string {
	return filepath.Join(m.cfg.WorkDirRoot, ".snapshots", snapshotID+".rootfs.ext4")
}

func (m *Manager) snapshotWorkspacePath(snapshotID string) string {
	return filepath.Join(m.cfg.WorkDirRoot, ".snapshots", snapshotID+".workspace.ext4")
}

func (m *Manager) snapshotDockerPath(snapshotID string) string {
	return filepath.Join(m.cfg.WorkDirRoot, ".snapshots", snapshotID+".docker.ext4")
}

// ForkWorkspaceImage snapshots the parent's rootfs, workspace, and docker images
// for the child lineage snapshot.
// The caller (Driver.CheckpointFork) is responsible for stopping the parent VM
// before calling this and restarting it afterward.
func (m *Manager) ForkWorkspaceImage(_ context.Context, parentWorkspaceID, childSnapshotID string) error {
	m.mu.RLock()
	parent, exists := m.instances[parentWorkspaceID]
	m.mu.RUnlock()

	var rootSrcPath, workspaceSrcPath, dockerSrcPath string
	if exists {
		rootSrcPath = parent.RootFSImage
		workspaceSrcPath = parent.WorkspaceImage
		dockerSrcPath = parent.DockerDataImage
	} else {
		// Parent not currently running; find images from its workdir.
		parentDir := filepath.Join(m.cfg.WorkDirRoot, parentWorkspaceID)
		rootSrcPath = filepath.Join(parentDir, "rootfs.ext4")
		workspaceSrcPath = filepath.Join(parentDir, "workspace.ext4")
		dockerSrcPath = filepath.Join(parentDir, "docker-data.ext4")
	}

	for _, p := range []struct {
		label string
		path  string
	}{
		{label: "rootfs", path: rootSrcPath},
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
		{label: "rootfs", src: rootSrcPath, dst: m.snapshotRootfsPath(childSnapshotID)},
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
