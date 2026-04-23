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
	// NexusBin is the path to the nexus binary (os.Executable()).
	// Used to spawn libkrun-vm child processes.
	NexusBin   string
	KernelPath string
	RootFSPath string
	WorkDirRoot string
	BasesDir   string
}

// Instance represents a running libkrun VM.
type Instance struct {
	WorkspaceID    string
	WorkDir        string
	WorkspaceImage string
	BaseImage      string
	SerialLog      string
	// GuestIP is the host-accessible SSH target (127.0.0.1:PORT).
	GuestIP        string
	// Process is the libkrun-vm child process.
	Process        *os.Process
	// Passt is the passt networking process.
	Passt          *passtState
	// SSHPort is the host port forwarded to guest port 22.
	SSHPort        int
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
	BasesDir        string
	SnapshotID      string
	MemoryMiB       int
	VCPUs           int
	HostConfigDrive string
}

// Spawn creates and starts a new libkrun VM.
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
	workspaceImagePath := filepath.Join(workDir, "workspace.ext4")
	workspaceOverlayPath := filepath.Join(workDir, "workspace-overlay.ext4")

	overlayMode := spec.BasesDir != ""
	var baseImagePath string

	if overlayMode {
		bp, err := EnsureBaseImage(spec.ProjectRoot, spec.BasesDir)
		if err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("ensure base image: %w", err)
		}
		baseImagePath = bp

		if snapID := strings.TrimSpace(spec.SnapshotID); snapID != "" {
			snapPath := m.snapshotImagePath(snapID)
			if err := copyFile(snapPath, workspaceOverlayPath); err != nil {
				os.RemoveAll(workDir)
				return nil, fmt.Errorf("restore overlay from fork snapshot: %w", err)
			}
		} else {
			if err := createWorkspaceOverlay(workspaceOverlayPath); err != nil {
				os.RemoveAll(workDir)
				return nil, fmt.Errorf("create workspace overlay: %w", err)
			}
		}
	} else if strings.TrimSpace(spec.SnapshotID) != "" {
		if err := copyFile(m.snapshotImagePath(spec.SnapshotID), workspaceImagePath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("restore workspace from snapshot: %w", err)
		}
	} else {
		if err := createWorkspaceImage(spec.ProjectRoot, workspaceImagePath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("build workspace image: %w", err)
		}
	}

	// Pick a free host port for SSH forwarding.
	sshPort, err := pickFreePort()
	if err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("pick ssh port: %w", err)
	}

	// Start passt (networking backend).
	ps, err := setupPasst(spec.WorkspaceID, workDir, sshPort)
	if err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("setup passt: %w", err)
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

	// Build kernel cmdline.
	// Re-use the same init binary as Firecracker so no guest changes are needed.
	workspaceImg := workspaceOverlayPath
	if !overlayMode {
		workspaceImg = workspaceImagePath
	}
	cmdline := buildKernelCmdline(overlayMode)

	vmSpec := VMSpec{
		WorkspaceID:     spec.WorkspaceID,
		KernelPath:      m.cfg.KernelPath,
		KernelCmdline:   cmdline,
		RootFSPath:      m.cfg.RootFSPath,
		WorkspaceImage:  workspaceImg,
		BaseImage:       baseImagePath,
		OverlayMode:     overlayMode,
		HostConfigDrive: spec.HostConfigDrive,
		MemoryMiB:       spec.MemoryMiB,
		VCPUs:           spec.VCPUs,
		SerialLog:       serialLog,
		PasstFDIndex:    0, // vmFD is ExtraFiles[0] → fd 3
		SSHHostPort:     sshPort,
		VsockPorts:      vsockPorts,
	}

	// Write VMSpec to a temp config file.
	configPath := filepath.Join(workDir, "libkrun-config.json")
	configData, err := json.Marshal(vmSpec)
	if err != nil {
		teardownPasst(ps)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("marshal vm spec: %w", err)
	}
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		teardownPasst(ps)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("write vm config: %w", err)
	}

	// Spawn the libkrun-vm child process.
	nexusBin := m.cfg.NexusBin
	if nexusBin == "" {
		nexusBin, err = os.Executable()
		if err != nil {
			teardownPasst(ps)
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("resolve nexus executable: %w", err)
		}
	}

	childCmd := exec.Command(nexusBin, "libkrun-vm", "--config="+configPath)
	childCmd.ExtraFiles = []*os.File{ps.vmFD} // vmFD becomes fd 3 in the child
	childCmd.Dir = workDir

	// Redirect child stdout/stderr to the VM's serial log.
	vmLogFile, err := os.OpenFile(serialLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		vmLogFile = os.Stderr
	}
	childCmd.Stdout = vmLogFile
	childCmd.Stderr = vmLogFile

	if err := childCmd.Start(); err != nil {
		teardownPasst(ps)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("spawn libkrun-vm child: %w", err)
	}
	_ = vmLogFile.Close()

	// The vmFD is now owned by the child; close in parent.
	_ = ps.vmFD.Close()
	ps.vmFD = nil

	// Write PID file.
	_ = os.WriteFile(filepath.Join(workDir, "libkrun.pid"), []byte(strconv.Itoa(childCmd.Process.Pid)), 0o600)

	inst := &Instance{
		WorkspaceID:    spec.WorkspaceID,
		WorkDir:        workDir,
		WorkspaceImage: workspaceImg,
		BaseImage:      baseImagePath,
		SerialLog:      serialLog,
		GuestIP:        fmt.Sprintf("127.0.0.1:%d", sshPort),
		Process:        childCmd.Process,
		Passt:          ps,
		SSHPort:        sshPort,
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
		return nil
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

	teardownPasst(inst.Passt)
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

// snapshotImagePath returns the path where a fork/snapshot image is stored.
func (m *Manager) snapshotImagePath(snapshotID string) string {
	return filepath.Join(m.cfg.WorkDirRoot, ".snapshots", snapshotID+".ext4")
}

// ForkWorkspaceImage copies the parent's workspace image as a snapshot for the child.
// The parent VM is stopped first and resumed afterwards via the driver.
func (m *Manager) ForkWorkspaceImage(_ context.Context, parentWorkspaceID, childSnapshotID string) error {
	m.mu.RLock()
	parent, exists := m.instances[parentWorkspaceID]
	m.mu.RUnlock()

	var srcPath string
	if exists {
		srcPath = parent.WorkspaceImage
	} else {
		// Parent not running; find the image from the workdir.
		workDir := filepath.Join(m.cfg.WorkDirRoot, parentWorkspaceID)
		// Try overlay path first, then legacy.
		p := filepath.Join(workDir, "workspace-overlay.ext4")
		if _, err := os.Stat(p); err == nil {
			srcPath = p
		} else {
			srcPath = filepath.Join(workDir, "workspace.ext4")
		}
	}

	snapshotsDir := filepath.Join(m.cfg.WorkDirRoot, ".snapshots")
	if err := os.MkdirAll(snapshotsDir, 0o755); err != nil {
		return fmt.Errorf("create snapshots dir: %w", err)
	}

	dstPath := m.snapshotImagePath(childSnapshotID)
	return copyFile(srcPath, dstPath)
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

// buildKernelCmdline returns the kernel command line for a libkrun VM.
// Matches the Firecracker cmdline so the same initrd/agent binary works.
func buildKernelCmdline(overlayMode bool) string {
	if raw := strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_BOOT_ARGS")); raw != "" {
		return raw
	}
	// libkrun uses a VirtIO console (hvc0), not a legacy ttyS0 UART.
	// Use "console=hvc0" so kernel output goes to the VirtIO console that
	// krun_set_console_output captures; keep ttyS0 as fallback for kernels
	// that don't have hvc0 compiled in.
	base := "console=hvc0 console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw init=/usr/local/bin/nexus-firecracker-agent"
	if overlayMode {
		base += " nexus.overlay=1"
	}
	return base
}
