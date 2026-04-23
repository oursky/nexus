package firecracker

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// tapSetupFunc creates a TAP device and attaches it to the bridge.
// Returns an opaque handle (unused in production, may be used in tests).
// Overridable in tests.
var tapSetupFunc func(tapName, hostIP, subnetCIDR string) (any, error) = realSetupTAP

// tapTeardownFunc tears down a TAP device.
// Overridable in tests.
var tapTeardownFunc func(tapName, subnetCIDR string) = realTeardownTAP

// tapPrepareFunc, if non-nil, is called before Firecracker starts to configure
// the exec.Cmd for user+network namespace isolation (rootless networking).
// Signature: (tapName string, cmd *exec.Cmd)
var tapPrepareFunc func(tapName string, cmd *exec.Cmd) = defaultTapPrepare

// tapAttachFunc, if non-nil, is called after Firecracker starts to attach
// userspace networking to its namespace.
// Signature: (workspaceID string, firecrackerPid int, workDir string, cid uint32) → error
var tapAttachFunc func(workspaceID string, firecrackerPid int, workDir string, cid uint32) error = defaultTapAttach

// tapNetTeardownFunc, if non-nil, tears down the per-VM network backend.
var tapNetTeardownFunc func(workspaceID string) = defaultTapNetTeardown

// tapHostDevNameFunc returns the device name that Firecracker's API expects for
// the network interface.  In rootless (slirp4netns) mode this is always "tap0"
// inside the VM's own netns; in legacy bridge mode it is the host tap name.
var tapHostDevNameFunc func(tap string) string = defaultHostDevName

// initialCID is the starting CID for guest VMs.
const initialCID uint32 = 1000

const VendingVSockPort uint32 = 10790

// SpawnSpec defines the configuration for spawning a new Firecracker VM.
type SpawnSpec struct {
	WorkspaceID string
	ProjectRoot string
	// BasesDir enables base-image mode when non-empty (production).
	// A shared base.ext4 is built once per repo and cached here.
	// XFS reflink: O(1) CoW clone per workspace → /dev/vdb R/W, no overlayfs.
	// When empty: legacy mode (tests) — full mkfs.ext4 -d copy → /dev/vdb R/W.
	BasesDir    string
	SnapshotID  string
	MemoryMiB   int
	VCPUs       int
	// HostConfigDrive is the path to a pre-built ext4 image containing host
	// config files (gitconfig, SSH material, tool configs).  Attached as /dev/vdc.
	HostConfigDrive string
}

// Instance represents a running Firecracker VM instance.
type Instance struct {
	WorkspaceID    string
	WorkDir        string
	WorkspaceImage string // writable image: overlay in overlay mode, full image in legacy mode
	BaseImage      string // shared read-only base image path (overlay mode only; empty in legacy)
	APISocket      string
	VSockPath      string
	SerialLog      string
	CID            uint32
	Process        *os.Process
	TAPName        string
	GuestIP        string
	HostIP         string
}

// ManagerConfig holds configuration for the Firecracker manager.
type ManagerConfig struct {
	FirecrackerBin string
	KernelPath     string
	RootFSPath     string
	WorkDirRoot    string
	// BasesDir is where shared per-repo base images are cached.
	// When non-empty, overlay mode is used for new workspaces.
	BasesDir string
}

// APIClientFactory creates API clients for instances.
type APIClientFactory func(sockPath string) apiClientInterface

// apiClientInterface defines the methods we need from the API client.
type apiClientInterface interface {
	put(ctx context.Context, path string, body any) error
	patch(ctx context.Context, path string, body any) error
	PauseVM(ctx context.Context) error
	ResumeVM(ctx context.Context) error
	CreateSnapshot(ctx context.Context, vmstatePath, memFilePath string) error
}

// networkCommandRunner runs a network-related command.
// Overridable in tests to avoid real network operations.
var networkCommandRunner = runNetworkCommand

// workspaceImageBuilderFunc creates a workspace image that is mounted at
// /workspace inside the guest. Overridable in tests.
var workspaceImageBuilderFunc = createWorkspaceImage

func runNetworkCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Manager handles Firecracker VM lifecycle operations.
type Manager struct {
	config           ManagerConfig
	instances        map[string]*Instance
	mu               sync.RWMutex
	nextCID          uint32
	apiClientFactory APIClientFactory
	snapshotCache    map[string]*baseSnapshot
	snapshotMu       sync.RWMutex
}

// NewManager creates a new Firecracker manager with the given configuration.
func NewManager(cfg ManagerConfig) *Manager {
	return newManager(cfg)
}

// newManager creates a new Firecracker manager with the given configuration.
func newManager(cfg ManagerConfig) *Manager {
	if strings.TrimSpace(cfg.WorkDirRoot) != "" {
		_ = os.MkdirAll(cfg.WorkDirRoot, 0o755)
	}
	return &Manager{
		config:           cfg,
		instances:        make(map[string]*Instance),
		nextCID:          initialCID,
		apiClientFactory: defaultAPIClientFactory,
		snapshotCache:    make(map[string]*baseSnapshot),
	}
}

func defaultAPIClientFactory(sockPath string) apiClientInterface {
	return newAPIClient(sockPath)
}

// guestMAC returns a deterministic MAC address for a given CID.
func guestMAC(cid uint32) string {
	b0 := byte((cid >> 8) & 0xFF)
	b1 := byte(cid & 0xFF)
	return fmt.Sprintf("AA:FC:00:00:%02X:%02X", b0, b1)
}

// setupTAP delegates to tapSetupFunc (real or mock in tests).
func setupTAP(tapNameStr, hostIP, subnetCIDR string) error {
	_, err := tapSetupFunc(tapNameStr, hostIP, subnetCIDR)
	return err
}

// teardownTAP delegates to tapTeardownFunc (real or mock in tests).
func teardownTAP(tapNameStr, subnetCIDR string) {
	tapTeardownFunc(tapNameStr, subnetCIDR)
}

// cleanup removes the workdir and cleans up a partially started instance.
func (m *Manager) cleanup(workDir string, proc *os.Process) {
	if proc != nil {
		proc.Kill()
		proc.Wait()
	}
	os.RemoveAll(workDir)
}

// Spawn creates and starts a new Firecracker VM instance.
func (m *Manager) Spawn(ctx context.Context, spec SpawnSpec) (*Instance, error) {
	// Phase 1: Quick duplicate check and CID allocation (mutex held briefly).
	m.mu.Lock()
	if _, exists := m.instances[spec.WorkspaceID]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("workspace already exists: %s", spec.WorkspaceID)
	}

	// Snapshot restore path (experiment-gated).
	if os.Getenv("NEXUS_FIRECRACKER_SNAPSHOT") == "1" {
		snap, err := m.ensureBaseSnapshot(ctx, m.config.KernelPath, m.config.RootFSPath)
		if err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("ensure base snapshot: %w", err)
		}
		if _, statErr := os.Stat(snap.vmstatePath); statErr == nil {
			m.mu.Unlock()
			inst, restoreErr := m.restoreFromSnapshot(ctx, spec, snap)
			if restoreErr == nil {
				m.mu.Lock()
				m.instances[spec.WorkspaceID] = inst
				m.mu.Unlock()
				return inst, nil
			}
			log.Printf("[firecracker] snapshot restore failed, falling back to cold boot: %v", restoreErr)
			key := snapshotCacheKey(m.config.KernelPath, m.config.RootFSPath)
			m.snapshotMu.Lock()
			delete(m.snapshotCache, key)
			m.snapshotMu.Unlock()
			m.mu.Lock()
		}
	}

	cid := m.nextCID
	m.nextCID++
	// Release mutex before slow I/O (mkfs.ext4, Firecracker launch, API config).
	// Other operations (Stop, Get, etc.) can proceed concurrently during image build.
	m.mu.Unlock()

	// Phase 2: Slow I/O — all of this runs WITHOUT the global mutex.
	workDir := filepath.Join(m.config.WorkDirRoot, spec.WorkspaceID)

	// Disk-space pre-flight: estimate how much space we need.
	const miB = int64(1024 * 1024)
	const overlayReserve = 512 * miB // just the sparse overlay header
	var neededBytes int64
	if spec.BasesDir != "" {
		neededBytes = overlayReserve // overlay itself is sparse; base is shared
	} else if strings.TrimSpace(spec.SnapshotID) != "" {
		info, statErr := os.Stat(m.snapshotImagePath(spec.SnapshotID))
		if statErr != nil {
			return nil, fmt.Errorf("stat snapshot image: %w", statErr)
		}
		neededBytes = info.Size() + 512*miB
	} else {
		size, sizeErr := directorySizeBytes(spec.ProjectRoot)
		if sizeErr != nil {
			return nil, fmt.Errorf("compute project size: %w", sizeErr)
		}
		neededBytes = workspaceImageSizeBytes(size) + 512*miB
	}
	if err := checkDiskSpace(m.config.WorkDirRoot, neededBytes); err != nil {
		return nil, fmt.Errorf("insufficient disk space for workspace: %w", err)
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workdir: %w", err)
	}

	apiSocket := filepath.Join(workDir, "firecracker.sock")
	vsockPath := filepath.Join(workDir, "vsock.sock")
	serialLog := filepath.Join(workDir, "firecracker.log")
	workspaceImagePath := filepath.Join(workDir, "workspace.ext4")

	// Derive a workspace-scoped tap name (used as registry key and for logging).
	// The actual TAP device inside the VM's user netns is always "tap0".
	tap := tapNameForWorkspace(spec.WorkspaceID)
	mac := guestMAC(cid)

	// Two workspace image modes:
	//
	// reflink  (BasesDir set, production): O(1) XFS CoW clone of the shared
	//          base.ext4 per workspace. /dev/vdb is the clone, mounted R/W.
	//          No overlayfs in VM. Docker overlay2 works natively.
	//
	// legacy   (BasesDir empty, tests only): full mkfs.ext4 -d copy.
	var baseImagePath string

	if spec.BasesDir != "" {
		bp, err := EnsureBaseImage(spec.ProjectRoot, spec.BasesDir)
		if err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("ensure base image: %w", err)
		}
		baseImagePath = bp

		src := baseImagePath
		if snapID := strings.TrimSpace(spec.SnapshotID); snapID != "" {
			src = m.snapshotImagePath(snapID) // fork: clone parent's workspace
		}
		log.Printf("[firecracker] reflink workspace %s ← %s", spec.WorkspaceID, src)
		if err := CreateWorkspaceReflink(src, workspaceImagePath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("reflink workspace image: %w", err)
		}
	} else if strings.TrimSpace(spec.SnapshotID) != "" {
		if err := copyFile(m.snapshotImagePath(spec.SnapshotID), workspaceImagePath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("failed to restore workspace image from snapshot: %w", err)
		}
	} else {
		if err := workspaceImageBuilderFunc(spec.ProjectRoot, workspaceImagePath); err != nil {
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("failed to build workspace image: %w", err)
		}
	}

	// Launch Firecracker. tapPrepareFunc may configure a user+network namespace
	// so that Firecracker can create its own TAP device without host privileges.
	cmd := exec.Command(
		m.config.FirecrackerBin,
		"--api-sock", apiSocket,
		"--id", spec.WorkspaceID,
	)
	cmd.Dir = workDir

	// Let the networking backend prepare the Firecracker command (e.g. set
	// CLONE_NEWUSER | CLONE_NEWNET so it gets its own unprivileged netns).
	tapPrepareFunc(tap, cmd)

	logFile, err := os.OpenFile(serialLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("failed to create firecracker log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("failed to start firecracker: %w", err)
	}
	_ = logFile.Close()

	firecrackerPid := cmd.Process.Pid
	pidPath := filepath.Join(workDir, "firecracker.pid")
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(firecrackerPid)), 0o600)

	// Attach userspace networking (slirp4netns or legacy tap-helper) AFTER
	// Firecracker starts, since slirp4netns needs the process PID to locate
	// its network namespace.
	if err := tapAttachFunc(spec.WorkspaceID, firecrackerPid, workDir, cid); err != nil {
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to attach network for %s: %w", tap, err)
	}

	if err := m.waitForAPISocket(ctx, apiSocket); err != nil {
		tapNetTeardownFunc(spec.WorkspaceID)
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to wait for API socket: %w", err)
	}

	client := m.apiClientFactory(apiSocket)

	machineConfig := map[string]any{
		"vcpu_count":        spec.VCPUs,
		"mem_size_mib":      spec.MemoryMiB,
		"smt":               false,
		"track_dirty_pages": false,
	}
	netTeardown := func() { tapNetTeardownFunc(spec.WorkspaceID) }

	if err := client.put(ctx, "/machine-config", machineConfig); err != nil {
		netTeardown()
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to configure machine: %w", err)
	}

	bootSource := map[string]any{
		"kernel_image_path": m.config.KernelPath,
		"boot_args":         defaultFirecrackerBootArgs(),
	}
	if err := client.put(ctx, "/boot-source", bootSource); err != nil {
		netTeardown()
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to configure boot source: %w", err)
	}

	driveConfig := map[string]any{
		"drive_id":       "rootfs",
		"path_on_host":   m.config.RootFSPath,
		"is_root_device": true,
		"is_read_only":   false,
	}
	if err := client.put(ctx, "/drives/rootfs", driveConfig); err != nil {
		netTeardown()
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to configure drive: %w", err)
	}

	// /dev/vdb = per-workspace ext4 image (read-write).
	// Reflink mode: O(1) XFS CoW clone of shared base.ext4.
	// Legacy mode (tests): full mkfs.ext4 -d copy.
	workspaceDriveConfig := map[string]any{
		"drive_id":       "workspace",
		"path_on_host":   "workspace.ext4",
		"is_root_device": false,
		"is_read_only":   false,
	}
	if err := client.put(ctx, "/drives/workspace", workspaceDriveConfig); err != nil {
		netTeardown()
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to configure workspace drive: %w", err)
	}

	// Host config drive on /dev/vdc.
	if spec.HostConfigDrive != "" {
		cfgDrive := map[string]any{
			"drive_id":       "hostconfig",
			"path_on_host":   spec.HostConfigDrive,
			"is_root_device": false,
			"is_read_only":   true,
		}
		if err := client.put(ctx, "/drives/hostconfig", cfgDrive); err != nil {
			log.Printf("[firecracker] warning: could not attach host config drive: %v", err)
		}
	}

	// Configure network interface: Firecracker opens the tap by name.
	// In rootless (slirp4netns) mode this is always "tap0" inside the VM's own
	// netns; in legacy mode it is the host-side tap name.
	netIfaceConfig := map[string]any{
		"iface_id":      "eth0",
		"host_dev_name": tapHostDevNameFunc(tap),
		"guest_mac":     mac,
	}
	if err := client.put(ctx, "/network-interfaces/eth0", netIfaceConfig); err != nil {
		netTeardown()
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to configure network interface: %w", err)
	}

	vsockConfig := map[string]any{
		"vsock_id":  "agent",
		"guest_cid": cid,
		// Relative path: Firecracker creates the socket in its working directory.
		// The host driver dials the absolute vsockPath stored in the Instance.
		"uds_path": "vsock.sock",
	}
	if err := client.put(ctx, "/vsock", vsockConfig); err != nil {
		netTeardown()
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to configure vsock: %w", err)
	}

	action := map[string]any{
		"action_type": "InstanceStart",
	}
	if err := client.put(ctx, "/actions", action); err != nil {
		netTeardown()
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("failed to start instance: %w", err)
	}

	// After successful cold boot, create base snapshot if experiment gate is active
	if os.Getenv("NEXUS_FIRECRACKER_SNAPSHOT") == "1" {
		snap, _ := m.ensureBaseSnapshot(ctx, m.config.KernelPath, m.config.RootFSPath)
		if snap != nil {
			if _, statErr := os.Stat(snap.vmstatePath); os.IsNotExist(statErr) {
				if pauseErr := client.PauseVM(ctx); pauseErr == nil {
					if snapErr := client.CreateSnapshot(ctx, snap.vmstatePath, snap.memFilePath); snapErr != nil {
						log.Printf("[firecracker] WARNING: failed to create base snapshot: %v", snapErr)
					}
					if resumeErr := client.ResumeVM(ctx); resumeErr != nil {
						log.Printf("[firecracker] WARNING: failed to resume after snapshot: %v", resumeErr)
					}
				} else {
					log.Printf("[firecracker] WARNING: failed to pause for base snapshot: %v", pauseErr)
				}
			}
		}
	}

	// Prefer the host-accessible SSH target from slirp4netns port forwarding.
	// Falls back to the VM's actual IP (only reachable inside the namespace).
	guestIPOrTarget := getSlirpSSHTarget(spec.WorkspaceID)
	if guestIPOrTarget == "" {
		guestIPOrTarget = guestIPForCID(cid)
	}

	inst := &Instance{
		WorkspaceID:    spec.WorkspaceID,
		WorkDir:        workDir,
		WorkspaceImage: workspaceImagePath, // reflink clone or legacy full image
		BaseImage:      baseImagePath,      // non-empty in reflink mode
		APISocket:      apiSocket,
		VSockPath:      vsockPath,
		SerialLog:      serialLog,
		CID:            cid,
		Process:        cmd.Process,
		TAPName:        tap,
		GuestIP:        guestIPOrTarget,
		HostIP:         bridgeGatewayIP(),
	}

	// Phase 3: Re-acquire mutex to register the instance.
	// Guard against the rare case where the workspace was removed and re-created
	// concurrently while mkfs/VM-boot was in progress.
	m.mu.Lock()
	if _, exists := m.instances[spec.WorkspaceID]; exists {
		m.mu.Unlock()
		netTeardown()
		m.cleanup(workDir, cmd.Process)
		return nil, fmt.Errorf("workspace already exists: %s", spec.WorkspaceID)
	}
	m.instances[spec.WorkspaceID] = inst
	m.mu.Unlock()
	return inst, nil
}

// defaultFirecrackerBootArgs returns the kernel command line for a VM.
// If NEXUS_FIRECRACKER_BOOT_ARGS is set, it is returned verbatim.
func defaultFirecrackerBootArgs() string {
	if raw := strings.TrimSpace(os.Getenv("NEXUS_FIRECRACKER_BOOT_ARGS")); raw != "" {
		return raw
	}
	return "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw init=/usr/local/bin/nexus-firecracker-agent"
}

// waitForAPISocket polls for the API socket to exist with the given context.
func (m *Manager) waitForAPISocket(ctx context.Context, path string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return nil
			}
		}
	}
}

// Stop terminates a running VM instance and cleans up resources.
func (m *Manager) Stop(ctx context.Context, workspaceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, exists := m.instances[workspaceID]
	if !exists {
		return fmt.Errorf("workspace not found: %s", workspaceID)
	}

	client := m.apiClientFactory(inst.APISocket)
	action := map[string]any{
		"action_type": "SendCtrlAltDel",
	}

	if err := client.put(ctx, "/actions", action); err != nil {
		if inst.Process != nil {
			inst.Process.Kill()
		}
	}

	if inst.Process != nil {
		waitDone := make(chan error, 1)
		go func() {
			_, err := inst.Process.Wait()
			waitDone <- err
		}()

		select {
		case <-ctx.Done():
			_ = inst.Process.Kill()
			<-waitDone
		case <-waitDone:
		}
	}

	// Teardown the network backend after the VM exits.
	tapNetTeardownFunc(workspaceID)

	os.RemoveAll(inst.WorkDir)

	delete(m.instances, workspaceID)

	return nil
}

// CleanupWorkspaceByID tears down workspace runtime state even when the
// instance is not currently tracked in-memory (e.g. after daemon restart).
func (m *Manager) CleanupWorkspaceByID(ctx context.Context, workspaceID string) error {
	if strings.TrimSpace(workspaceID) == "" {
		return nil
	}

	m.mu.RLock()
	_, tracked := m.instances[workspaceID]
	m.mu.RUnlock()
	if tracked {
		return m.Stop(ctx, workspaceID)
	}

	workDir := filepath.Join(m.config.WorkDirRoot, workspaceID)

	pid := 0
	if data, err := os.ReadFile(filepath.Join(workDir, "firecracker.pid")); err == nil {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
			pid = parsed
		}
	}
	if pid <= 0 {
		pid = firecrackerPIDForWorkspace(workspaceID)
	}
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
	}

	tapNetTeardownFunc(workspaceID)
	if err := os.RemoveAll(workDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func firecrackerPIDForWorkspace(workspaceID string) int {
	pattern := fmt.Sprintf("firecracker --api-sock .*%s/firecracker.sock --id %s", workspaceID, workspaceID)
	out, err := exec.Command("pgrep", "-f", pattern).CombinedOutput()
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if line == "" {
		return 0
	}
	pid, err := strconv.Atoi(line)
	if err != nil {
		return 0
	}
	return pid
}

// GrowWorkspace grows the workspace backing image to newSizeBytes and notifies
// Firecracker via PATCH /drives/workspace so the guest can online-resize.
// The caller must run `resize2fs /dev/vdb` in the guest after this returns.
func (m *Manager) GrowWorkspace(ctx context.Context, workspaceID string, newSizeBytes int64) error {
	m.mu.Lock()
	inst, ok := m.instances[workspaceID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("workspace not found: %s", workspaceID)
	}

	if err := os.Truncate(inst.WorkspaceImage, newSizeBytes); err != nil {
		return fmt.Errorf("grow workspace image: %w", err)
	}

	client := m.apiClientFactory(inst.APISocket)
	patch := map[string]any{
		"drive_id":     "workspace",
		"path_on_host": inst.WorkspaceImage,
		"is_read_only": false,
	}
	if err := client.patch(ctx, "/drives/workspace", patch); err != nil {
		return fmt.Errorf("patch firecracker drive: %w", err)
	}

	return nil
}

// ReconcileOrphans scans WorkDirRoot for leftover Firecracker VM directories
// from previous daemon runs and cleans up those whose process is no longer alive.
// Directories belonging to live workspaceIDs whose process is still running are
// left in place and logged.
func (m *Manager) ReconcileOrphans(ctx context.Context, liveWorkspaceIDs map[string]struct{}) error {
	if strings.TrimSpace(m.config.WorkDirRoot) == "" {
		return nil
	}

	entries, err := os.ReadDir(m.config.WorkDirRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reconcile orphans: readdir %s: %w", m.config.WorkDirRoot, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wsID := entry.Name()

		m.mu.RLock()
		_, alreadyRegistered := m.instances[wsID]
		m.mu.RUnlock()
		if alreadyRegistered {
			continue
		}

		workDir := filepath.Join(m.config.WorkDirRoot, wsID)

		pidData, readErr := os.ReadFile(filepath.Join(workDir, "firecracker.pid"))
		pid := 0
		if readErr == nil {
			if p, parseErr := strconv.Atoi(strings.TrimSpace(string(pidData))); parseErr == nil {
				pid = p
			}
		}

		alive := pid > 0 && processAlive(pid)

		if alive {
			if _, isLive := liveWorkspaceIDs[wsID]; isLive {
				log.Printf("firecracker reconcile: workspace %s process %d still running, skipping re-attach", wsID, pid)
				continue
			}
			proc, findErr := os.FindProcess(pid)
			if findErr == nil {
				_ = proc.Kill()
				_, _ = proc.Wait()
			}
		}

		tapNetTeardownFunc(wsID)
		if removeErr := os.RemoveAll(workDir); removeErr != nil {
			log.Printf("firecracker reconcile: remove workdir %s: %v", workDir, removeErr)
		} else {
			log.Printf("firecracker reconcile: cleaned orphaned workspace %s", wsID)
		}
	}

	return nil
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// Get retrieves an instance by workspace ID.
func (m *Manager) Get(workspaceID string) (*Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inst, exists := m.instances[workspaceID]
	if !exists {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}

	return inst, nil
}

func checkDiskSpace(dir string, needed int64) error {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return nil
	}
	avail := int64(s.Bavail) * int64(s.Bsize)
	if avail < needed {
		return fmt.Errorf("need %d MiB, only %d MiB free in %s", needed>>20, avail>>20, dir)
	}
	return nil
}
