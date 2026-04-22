package firecracker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type baseSnapshot struct {
	vmstatePath string
	memFilePath string
	kernelPath  string
	rootfsPath  string
}

// snapshotCacheKey returns a deterministic key for a (kernelPath, rootfsPath) pair.
func snapshotCacheKey(kernelPath, rootfsPath string) string {
	h := sha256.Sum256([]byte(kernelPath + "|" + rootfsPath))
	return hex.EncodeToString(h[:])[:16]
}

// ensureBaseSnapshot returns the cached base snapshot for the given kernel+rootfs pair.
// If no snapshot exists yet in the cache, it creates a placeholder entry. The actual
// VM state files are written when a VM cold-boots (see Spawn). Callers must check
// whether snap.vmstatePath exists on disk before using snapshot restore.
func (m *Manager) ensureBaseSnapshot(ctx context.Context, kernelPath, rootfsPath string) (*baseSnapshot, error) {
	key := snapshotCacheKey(kernelPath, rootfsPath)

	m.snapshotMu.RLock()
	snap, ok := m.snapshotCache[key]
	m.snapshotMu.RUnlock()
	if ok {
		return snap, nil
	}

	snapshotsDir := filepath.Join(m.config.WorkDirRoot, ".snapshots")
	baseDir := filepath.Join(snapshotsDir, "base-"+key)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create base snapshot dir: %w", err)
	}

	snap = &baseSnapshot{
		vmstatePath: filepath.Join(baseDir, "vm.snap"),
		memFilePath: filepath.Join(baseDir, "mem.file"),
		kernelPath:  kernelPath,
		rootfsPath:  rootfsPath,
	}

	m.snapshotMu.Lock()
	m.snapshotCache[key] = snap
	m.snapshotMu.Unlock()

	return snap, nil
}

// CheckpointForkSnapshot pauses the parent VM, creates a VM state snapshot plus
// a CoW copy of the workspace image for the child, then resumes the parent.
// Returns a snapshotID that can be used by restoreFromSnapshot to spawn the child.
// ResumeVM is always called, even if snapshot creation fails.
func (m *Manager) CheckpointForkSnapshot(ctx context.Context, workspaceID, childWorkspaceID string) (string, error) {
	m.mu.RLock()
	parent, exists := m.instances[workspaceID]
	m.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("workspace not found: %s", workspaceID)
	}
	if strings.TrimSpace(parent.WorkspaceImage) == "" {
		return "", fmt.Errorf("workspace image missing for %s", workspaceID)
	}

	client := m.apiClientFactory(parent.APISocket)

	if err := client.PauseVM(ctx); err != nil {
		return "", fmt.Errorf("pause parent VM: %w", err)
	}

	forkDirName := "fork-" + workspaceID + "-" + childWorkspaceID
	forkDir := filepath.Join(m.config.WorkDirRoot, ".snapshots", forkDirName)
	if err := os.MkdirAll(forkDir, 0o755); err != nil {
		_ = client.ResumeVM(ctx)
		return "", fmt.Errorf("create fork dir: %w", err)
	}

	vmstatePath := filepath.Join(forkDir, "vm.snap")
	memFilePath := filepath.Join(forkDir, "mem.file")

	snapErr := client.CreateSnapshot(ctx, vmstatePath, memFilePath)

	resumeErr := client.ResumeVM(ctx)
	if snapErr != nil {
		if resumeErr != nil {
			log.Printf("[firecracker] WARNING: resume failed after snapshot error for %s: %v", workspaceID, resumeErr)
		}
		return "", fmt.Errorf("create fork snapshot: %w", snapErr)
	}
	if resumeErr != nil {
		return "", fmt.Errorf("resume parent VM after fork snapshot: %w", resumeErr)
	}

	childImg := filepath.Join(forkDir, "workspace.ext4")
	if err := copyFile(parent.WorkspaceImage, childImg); err != nil {
		return "", fmt.Errorf("copy workspace image for fork: %w", err)
	}

	snapshotID := forkDirName
	return snapshotID, nil
}

// restoreFromSnapshot spawns a new Firecracker VM by restoring from a previously
// created base snapshot. The restored VM uses CoW copies of the snapshot's memory
// and rootfs files, plus a freshly built workspace image.
//
// This method takes m.mu.Lock() internally. Callers holding m.mu must release it
// before calling this method, then re-acquire after.
func (m *Manager) restoreFromSnapshot(ctx context.Context, spec SpawnSpec, snap *baseSnapshot) (*Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.instances[spec.WorkspaceID]; exists {
		return nil, fmt.Errorf("workspace already exists: %s", spec.WorkspaceID)
	}

	workDir := filepath.Join(m.config.WorkDirRoot, spec.WorkspaceID)

	size, sizeErr := directorySizeBytes(spec.ProjectRoot)
	if sizeErr != nil {
		return nil, fmt.Errorf("compute project size: %w", sizeErr)
	}
	const miB = int64(1024 * 1024)
	neededBytes := workspaceImageSizeBytes(size) + 512*miB
	if err := checkDiskSpace(m.config.WorkDirRoot, neededBytes); err != nil {
		return nil, fmt.Errorf("insufficient disk space for workspace: %w", err)
	}

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create workdir: %w", err)
	}

	apiSocket := filepath.Join(workDir, "firecracker.sock")
	vsockPath := filepath.Join(workDir, "vsock.sock")
	serialLog := filepath.Join(workDir, "firecracker.log")
	workspaceImagePath := filepath.Join(workDir, "workspace.ext4")

	cid := m.nextCID
	m.nextCID++

	tap := tapNameForWorkspace(spec.WorkspaceID)
	mac := guestMAC(cid)
	hostIP := bridgeGatewayIP()
	subnetCIDR := guestSubnetCIDR()

	if err := setupTAP(tap, hostIP, subnetCIDR); err != nil {
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("failed to setup tap %s: %w", tap, err)
	}

	memOverlay := filepath.Join(workDir, "mem.file")
	if err := copyFile(snap.memFilePath, memOverlay); err != nil {
		teardownTAP(tap, subnetCIDR)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("copy memory snapshot: %w", err)
	}

	rootfsOverlay := filepath.Join(workDir, "rootfs.ext4")
	if err := copyFile(snap.rootfsPath, rootfsOverlay); err != nil {
		teardownTAP(tap, subnetCIDR)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("copy rootfs: %w", err)
	}

	if err := workspaceImageBuilderFunc(spec.ProjectRoot, workspaceImagePath); err != nil {
		teardownTAP(tap, subnetCIDR)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("failed to build workspace image: %w", err)
	}

	restoreCfg := map[string]any{
		"load_snapshot": map[string]any{
			"snapshot_path": snap.vmstatePath,
			"mem_file_path": memOverlay,
		},
		"machine-config": map[string]any{
			"vcpu_count":   spec.VCPUs,
			"mem_size_mib": spec.MemoryMiB,
		},
		"drives": []map[string]any{
			{
				"drive_id":       "rootfs",
				"path_on_host":   "rootfs.ext4",
				"is_root_device": true,
				"is_read_only":   false,
			},
			{
				"drive_id":       "workspace",
				"path_on_host":   "workspace.ext4",
				"is_root_device": false,
				"is_read_only":   false,
			},
		},
		"network-interfaces": []map[string]any{
			{
				"iface_id":      "eth0",
				"host_dev_name": tap,
				"guest_mac":     mac,
			},
		},
		"vsock": map[string]any{
			"vsock_id":  "agent",
			"guest_cid": cid,
			"uds_path":  "vsock.sock",
		},
	}

	cfgBytes, err := json.Marshal(restoreCfg)
	if err != nil {
		teardownTAP(tap, subnetCIDR)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("marshal restore config: %w", err)
	}
	cfgPath := filepath.Join(workDir, "restore-config.json")
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		teardownTAP(tap, subnetCIDR)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("write restore config: %w", err)
	}

	cmd := exec.Command(
		m.config.FirecrackerBin,
		"--api-sock", apiSocket,
		"--id", spec.WorkspaceID,
		"--config-file", cfgPath,
	)
	cmd.Dir = workDir
	logFile, err := os.OpenFile(serialLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		teardownTAP(tap, subnetCIDR)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("failed to create firecracker log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		teardownTAP(tap, subnetCIDR)
		os.RemoveAll(workDir)
		return nil, fmt.Errorf("failed to start firecracker (restore): %w", err)
	}
	_ = logFile.Close()

	pidPath := filepath.Join(workDir, "firecracker.pid")
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)

	inst := &Instance{
		WorkspaceID:    spec.WorkspaceID,
		WorkDir:        workDir,
		WorkspaceImage: workspaceImagePath,
		APISocket:      apiSocket,
		VSockPath:      vsockPath,
		SerialLog:      serialLog,
		CID:            cid,
		Process:        cmd.Process,
		TAPName:        tap,
		GuestIP:        "",
		HostIP:         hostIP,
	}

	m.instances[spec.WorkspaceID] = inst
	log.Printf("[firecracker] restored workspace %s from snapshot", spec.WorkspaceID)
	return inst, nil
}

// snapshotImagePath returns the filesystem path for an image-based snapshot.
// The fork-based snapshot path (fork-parent-child/workspace.ext4) is used by
// CheckpointForkSnapshot; this covers the legacy flat .ext4 format.
func (m *Manager) snapshotImagePath(snapshotID string) string {
	return filepath.Join(m.config.WorkDirRoot, ".snapshots", strings.TrimSpace(snapshotID)+".ext4")
}

// ForkWorkspaceImage copies the parent's workspace.ext4 into the snapshot store
// under childSnapshotID so the child VM boots with identical /workspace content.
//
// For a running parent VM the procedure is:
//  1. Run `sync` inside the guest so all dirty page-cache writes are flushed
//     to the virtio-blk device before we touch the image.
//  2. Pause the VM so no further writes can race with the copy.
//  3. Copy workspace.ext4 to the snapshot store.
//  4. Resume the VM.  Resume is always called, even on error.
//
// For a stopped parent the image is copied directly from its workdir.
// The result is stored at snapshotImagePath(childSnapshotID) so that Spawn()
// picks it up via the SnapshotID field of SpawnSpec.
func (m *Manager) ForkWorkspaceImage(ctx context.Context, parentWorkspaceID, childSnapshotID string) error {
	m.mu.RLock()
	parentInst, running := m.instances[parentWorkspaceID]
	m.mu.RUnlock()

	// Determine source path and API client for live VMs.
	var srcPath string
	var apiClient apiClientInterface

	if running && parentInst.WorkspaceImage != "" {
		if _, err := os.Stat(parentInst.WorkspaceImage); err == nil {
			srcPath = parentInst.WorkspaceImage
			apiClient = m.apiClientFactory(parentInst.APISocket)
		}
	}
	if srcPath == "" {
		// Parent VM is stopped — use the workdir image directly.
		candidate := filepath.Join(m.config.WorkDirRoot, parentWorkspaceID, "workspace.ext4")
		if _, err := os.Stat(candidate); err == nil {
			srcPath = candidate
		}
	}
	if srcPath == "" {
		return fmt.Errorf("no workspace image found for parent %s", parentWorkspaceID)
	}

	if apiClient != nil {
		// Step 1: flush guest page cache to the virtio-blk device.
		// If sync fails we log and continue — a partial flush is better than none.
		m.mu.RLock()
		vsockPath := parentInst.VSockPath
		m.mu.RUnlock()
		if vsockPath != "" {
			if err := syncGuestFilesystem(ctx, vsockPath); err != nil {
				log.Printf("[firecracker] ForkWorkspaceImage: guest sync warning (%s): %v", parentWorkspaceID, err)
			}
		}

		// Step 2: pause the VM so no writes race with the copy.
		if err := apiClient.PauseVM(ctx); err != nil {
			return fmt.Errorf("pause parent VM for fork: %w", err)
		}
		// Step 4 (deferred): always resume, even on error.
		defer func() {
			if err := apiClient.ResumeVM(ctx); err != nil {
				log.Printf("[firecracker] WARNING: resume parent %s after fork failed: %v", parentWorkspaceID, err)
			}
		}()
	}

	// Step 3: copy the image.
	dst := m.snapshotImagePath(childSnapshotID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create snapshot dir: %w", err)
	}
	if err := copyFile(srcPath, dst); err != nil {
		return fmt.Errorf("copy workspace image for fork: %w", err)
	}

	fi, _ := os.Stat(dst)
	sz := int64(0)
	if fi != nil {
		sz = fi.Size()
	}
	log.Printf("[firecracker] ForkWorkspaceImage: %s → %s (%.1f GiB)", srcPath, dst, float64(sz)/(1<<30))
	return nil
}

// syncGuestFilesystem dials the guest agent over the Firecracker vsock proxy
// and runs `sync` to flush all dirty page-cache entries to the virtio-blk
// device.  This must be called before pausing the VM to ensure the workspace
// image on the host is fully up-to-date.
//
// The Firecracker vsock multiplexer requires a CONNECT <port> handshake over
// the host-side Unix socket before the connection is forwarded to the guest.
func syncGuestFilesystem(ctx context.Context, vsockPath string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(timeoutCtx, "unix", vsockPath)
	if err != nil {
		return fmt.Errorf("dial vsock for sync: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	// Firecracker vsock handshake: write CONNECT <port>\n, expect OK <port>\n.
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", DefaultAgentVSockPort); err != nil {
		return fmt.Errorf("vsock handshake write: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 32)
	n, err := conn.Read(buf)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("vsock handshake read: %w", err)
	}
	if resp := strings.TrimSpace(string(buf[:n])); !strings.HasPrefix(resp, "OK") {
		return fmt.Errorf("vsock handshake rejected: %s", resp)
	}

	client := NewAgentClient(conn)
	result, err := client.Exec(timeoutCtx, ExecRequest{
		ID:      "fork-sync",
		Command: "sync",
	})
	if err != nil {
		return fmt.Errorf("exec sync: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("sync exited %d: %s", result.ExitCode, result.Stderr)
	}
	return nil
}
