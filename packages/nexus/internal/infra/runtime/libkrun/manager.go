//go:build linux

package libkrun

import (
	"context"
	"fmt"
	"log"
	"os"
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

// DockerCredHelperVSockPort is the vsock port for the Docker credential helper proxy.
// The host listens; the guest dials to forward docker-credential requests.
const DockerCredHelperVSockPort uint32 = 10793

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
	WorkspaceImage  string // ext4 mounted at /workspace (fork/snapshot source of truth)
	DockerDataImage string // sparse ext4 for /var/lib/docker
	RootfsImage     string // per-workspace rootfs.ext4 (writable clone of base rootfs)
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
	// vmDirtyFlagFileName is written when a VM spawns and deleted only on a
	// clean Stop(). Its presence means the VM's disk images may still have
	// unflushed kernel page-cache dirty pages and must be fsync'd before any
	// snapshot copy (fork, checkpoint, etc.).
	vmDirtyFlagFileName = "vm-dirty.flag"
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

// PromoteWorkspaceImageFromProjectRoot refreshes the workspace base image from
// the current host project root state.
//
// In the hybrid overlay model this updates workspace-base.ext4 (the read-only
// baked lowerdir). For legacy workspaces without a base image it falls back to
// updating workspace.ext4 directly.
//
// Callers should stop the parent VM first to establish a clear snapshot boundary.
func (m *Manager) PromoteWorkspaceImageFromProjectRoot(ctx context.Context, workspaceID, projectRoot string) error {
	root := strings.TrimSpace(projectRoot)
	if workspaceID == "" {
		return fmt.Errorf("workspaceID is required")
	}
	if root == "" {
		return fmt.Errorf("project root is required")
	}

	manifestHash := resolveManifestHash(root)
	basePath, err := EnsureBaseImage(ctx, root, m.cfg.BasesDir, manifestHash)
	if err != nil {
		return fmt.Errorf("ensure base workspace image: %w", err)
	}

	workDir := filepath.Join(m.cfg.WorkDirRoot, workspaceID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create workspace dir %s: %w", workDir, err)
	}

	// In hybrid overlay mode the baked base always lives in workspace-base.ext4.
	workspaceBasePath := filepath.Join(workDir, "workspace-base.ext4")

	tmpPath := workspaceBasePath + ".promote.tmp"
	_ = os.Remove(tmpPath)
	if err := copyFileWithContext(ctx, basePath, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("promote workspace clone %s -> %s: %w", basePath, tmpPath, err)
	}
	if err := os.Rename(tmpPath, workspaceBasePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("promote workspace image: %w", err)
	}
	return nil
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

	var workspaceSrcPath, dockerSrcPath, rootfsSrcPath string
	if exists {
		workspaceSrcPath = parent.WorkspaceImage
		dockerSrcPath = parent.DockerDataImage
		rootfsSrcPath = parent.RootfsImage
	} else {
		// Parent not currently running; find images from its workdir.
		parentDir := filepath.Join(m.cfg.WorkDirRoot, parentWorkspaceID)
		workspaceSrcPath = filepath.Join(parentDir, "workspace.ext4")
		dockerSrcPath = filepath.Join(parentDir, "docker-data.ext4")
		rootfsSrcPath = filepath.Join(parentDir, "rootfs.ext4")

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
		// Materialize XFS unwritten extents before copying. Stopping a libkrun
		// VM via SIGINT/SIGKILL does not cause the guest OS to cleanly unmount
		// its filesystems. The kernel retains dirty mmap page-cache pages for
		// the virtio-blk backing files even after the process exits. A simple
		// fsync flushes those dirty pages but does NOT convert XFS "unwritten"
		// (preallocated) extents to written extents — the data lives only in
		// page cache. When we snapshot via FICLONE the clone shares the same
		// unwritten XFS extents but not the page cache, so the forked VM would
		// read zeros from disk. materializeExtents does a read-then-write pass
		// to force the kernel to realise all page-cache data into real disk
		// blocks, converting unwritten extents to written ones, before we
		// FICLONE the result.
		log.Printf("[libkrun] ForkWorkspaceImage: materializing %s image extents before copy", p.label)
		if err := materializeExtents(p.path); err != nil {
			log.Printf("[libkrun] ForkWorkspaceImage: materializeExtents %s: %v (continuing)", p.label, err)
		}
	}

	// rootfsSrcPath is resolved above (from running instance or workdir) so the
	// compiler is satisfied; rootfs is always re-cloned from the base on spawn
	// and is not included in the fork snapshot.
	_ = rootfsSrcPath

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
