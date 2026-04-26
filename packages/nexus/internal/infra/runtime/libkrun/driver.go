//go:build linux

package libkrun

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
)

// Driver implements the libkrun runtime driver.
// It mirrors the historical VM driver layout (workspace-scoped state paths).
type Driver struct {
	manager      *Manager
	projectRoots map[string]string
	snapshotIDs  map[string]string
	readyTools   map[string]bool
	mu           sync.RWMutex
}

// NewDriver creates a new libkrun Driver.
func NewDriver(cfg ManagerConfig, opts ...DriverOption) *Driver {
	mgr := NewManager(cfg)
	d := &Driver{
		manager:      mgr,
		projectRoots: make(map[string]string),
		snapshotIDs:  make(map[string]string),
		readyTools:   make(map[string]bool),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DriverOption is a functional option for Driver.
type DriverOption func(*Driver)

// Backend returns the driver backend identifier.
func (d *Driver) Backend() string { return "libkrun" }

// SerialLogPath returns the path to the libkrun VM console log (hvc0) for a
// running workspace. The hvc0 file contains the guest kernel/agent output and
// is much smaller than the host-side libkrun.log which contains verbose VMM
// debug output. Returns an error if the workspace is not found.
func (d *Driver) SerialLogPath(workspaceID string) (string, error) {
	inst, err := d.manager.Get(workspaceID)
	if err != nil {
		return "", fmt.Errorf("workspace %s: %w", workspaceID, err)
	}
	return inst.SerialLog + ".hvc0", nil
}

// GuestSSHHost returns the SSH address for a running workspace VM.
func (d *Driver) GuestSSHHost(_ context.Context, workspaceID string) (string, bool) {
	inst, err := d.manager.Get(workspaceID)
	if err != nil || inst == nil {
		return "", false
	}
	if ip := strings.TrimSpace(inst.GuestIP); ip != "" {
		return ip, true
	}
	return "", false
}

// CleanupStaleInstances removes orphaned state on daemon startup.
func (d *Driver) CleanupStaleInstances(ctx context.Context) error {
	return d.manager.ReconcileOrphans(ctx, map[string]struct{}{})
}

// GuestWorkdir returns the in-guest working directory for a workspace.
func (d *Driver) GuestWorkdir(_ string) string { return "/workspace" }

// ResolveWorkspaceEndpoint returns the host:port for a service inside a workspace.
func (d *Driver) ResolveWorkspaceEndpoint(_ context.Context, workspaceID string, remotePort int) (string, int, error) {
	if remotePort <= 0 {
		return "", 0, fmt.Errorf("remote port must be > 0")
	}
	inst, err := d.manager.Get(workspaceID)
	if err != nil || inst == nil {
		return "", 0, fmt.Errorf("workspace not running: %s", workspaceID)
	}
	// With libkrun+passt, we proxy ports via the agent spotlight protocol,
	// not via direct IP routing to the guest.
	return "127.0.0.1", remotePort, nil
}

// DialPort connects to a service port inside the workspace VM.
func (d *Driver) DialPort(ctx context.Context, workspaceID string, remotePort int) (net.Conn, error) {
	return d.dialSpotlightPort(ctx, workspaceID, remotePort)
}

// dialSpotlightPort uses the spotlight vsock proxy to reach a service inside the VM.
func (d *Driver) dialSpotlightPort(ctx context.Context, workspaceID string, guestPort int) (net.Conn, error) {
	if guestPort <= 0 || guestPort > 65535 {
		return nil, fmt.Errorf("guestPort %d out of range", guestPort)
	}
	inst, err := d.manager.Get(workspaceID)
	if err != nil || inst == nil {
		return nil, fmt.Errorf("workspace not running: %s", workspaceID)
	}

	spotlightSock := filepath.Join(inst.WorkDir, fmt.Sprintf("vsock_%d.sock", DefaultSpotlightVSockPort))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", spotlightSock)
	if err != nil {
		return nil, fmt.Errorf("spotlight dial: %w", err)
	}

	if _, err := fmt.Fprintf(conn, "FORWARD %d\n", guestPort); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("spotlight FORWARD write: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("spotlight FORWARD response: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(resp), "OK") {
		_ = conn.Close()
		return nil, fmt.Errorf("spotlight FORWARD rejected: %s", strings.TrimSpace(resp))
	}
	return conn, nil
}

// AgentConn opens a connection to the guest agent for the given workspace.
// libkrun creates the vsock_10789.sock Unix socket (listen=true); we dial it.
func (d *Driver) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	inst, err := d.manager.Get(workspaceID)
	if err != nil || inst == nil {
		return nil, fmt.Errorf("workspace not running: %s", workspaceID)
	}

	agentSock := filepath.Join(inst.WorkDir, fmt.Sprintf("vsock_%d.sock", DefaultAgentVSockPort))

	// Wait for libkrun to create the agent socket (VM may still be booting).
	deadline := time.Now().Add(15 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	var lastErr error
	for time.Now().Before(deadline) {
		dialer := &net.Dialer{Timeout: 2 * time.Second}
		conn, dialErr := dialer.DialContext(ctx, "unix", agentSock)
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("agent socket %s not ready: %w", agentSock, lastErr)
}

// Create registers a workspace (lazy — no VM start yet).
func (d *Driver) Create(_ context.Context, workspaceID, projectRoot string, options map[string]string) error {
	if projectRoot == "" {
		return errors.New("project root is required")
	}
	d.mu.Lock()
	d.projectRoots[workspaceID] = projectRoot
	if options != nil {
		if snap := strings.TrimSpace(options["lineage_snapshot_id"]); snap != "" {
			d.snapshotIDs[workspaceID] = snap
		}
	}
	d.mu.Unlock()
	log.Printf("[libkrun] registered workspace %s (lazy start)", workspaceID)
	return nil
}

// EnsureStarted starts the VM if not already running.
func (d *Driver) EnsureStarted(ctx context.Context, workspaceID, projectRoot string) error {
	if _, err := d.manager.Get(workspaceID); err == nil {
		return nil // already running
	}

	root := strings.TrimSpace(projectRoot)
	if root == "" {
		d.mu.RLock()
		root = strings.TrimSpace(d.projectRoots[workspaceID])
		d.mu.RUnlock()
	}
	if root == "" {
		return fmt.Errorf("workspace %s has no project root", workspaceID)
	}

	d.mu.Lock()
	d.projectRoots[workspaceID] = root
	d.mu.Unlock()

	d.mu.RLock()
	snapshotID := d.snapshotIDs[workspaceID]
	d.mu.RUnlock()

	memMiB := defaultMemMiB()

	// Build host config drive (gitconfig, SSH keys, tool auth).
	// Use a per-workspace path in the work dir so concurrent workspace starts
	// don't reformat each other's drive (the old project-root path was shared).
	home, _ := os.UserHomeDir()
	configDriveDir := filepath.Join(d.manager.cfg.WorkDirRoot, workspaceID)
	if err := os.MkdirAll(configDriveDir, 0o755); err != nil {
		log.Printf("[libkrun] warning: create config drive dir: %v", err)
	}
	configDrivePath := filepath.Join(configDriveDir, "host-config.ext4")
	if err := buildHostConfigDriveLibkrun(home, configDrivePath); err != nil {
		log.Printf("[libkrun] warning: host config drive: %v", err)
		configDrivePath = ""
	}

	spec := SpawnSpec{
		WorkspaceID:     workspaceID,
		ProjectRoot:     root,
		MemoryMiB:       memMiB,
		VCPUs:           1,
		SnapshotID:      snapshotID,
		HostConfigDrive: configDrivePath,
		VMProfile:       resolveWorkspaceVMProfile(root),
	}

	d.mu.Lock()
	delete(d.readyTools, workspaceID)
	d.mu.Unlock()

	if _, err := d.manager.Spawn(ctx, spec); err != nil {
		return err
	}
	return nil
}

func resolveWorkspaceVMProfile(projectRoot string) string {
	const fallback = "default"
	cfg, ok, err := config.LoadNexusfile(projectRoot)
	if err != nil || !ok {
		return fallback
	}
	profile := strings.ToLower(strings.TrimSpace(cfg.VM.Profile))
	switch profile {
	case "", "default":
		return fallback
	case "minimal":
		return profile
	case "dev":
		// Backward-compat for older Nexusfile values.
		return fallback
	default:
		return fallback
	}
}

// WorkspaceReady probes the guest agent to verify tools are installed.
func (d *Driver) WorkspaceReady(ctx context.Context, workspaceID string) (bool, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return false, errors.New("workspace ID is required")
	}

	d.mu.RLock()
	ready := d.readyTools[workspaceID]
	d.mu.RUnlock()
	if ready {
		return true, nil
	}

	// Keep readiness bounded and robust: once agent connection is reachable,
	// allow workspace to transition to running so PTY/exec/shell are unblocked.
	start := time.Now()
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	log.Printf("[libkrun] readiness: agent dial start workspace=%s timeout=8s", workspaceID)
	conn, err := d.AgentConn(probeCtx, workspaceID)
	if err != nil {
		log.Printf("[libkrun] readiness: agent dial not-ready workspace=%s duration=%s err=%v", workspaceID, time.Since(start).Round(time.Millisecond), err)
		return false, nil
	}
	_ = conn.Close()

	d.mu.Lock()
	d.readyTools[workspaceID] = true
	d.mu.Unlock()
	log.Printf("[libkrun] readiness: agent reachable workspace=%s duration=%s", workspaceID, time.Since(start).Round(time.Millisecond))
	return true, nil
}

// GrowWorkspace grows the workspace disk image and notifies the guest.
func (d *Driver) GrowWorkspace(ctx context.Context, workspaceID string, newSizeBytes int64) error {
	if err := d.manager.GrowWorkspace(ctx, workspaceID, newSizeBytes); err != nil {
		return err
	}

	conn, err := d.AgentConn(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("agent connect for disk.grow: %w", err)
	}
	defer conn.Close()

	result, err := agentExec(ctx, conn, AgentExecRequest{
		ID:   fmt.Sprintf("disk-grow-%d", time.Now().UnixNano()),
		Type: "disk.grow",
	})
	if err != nil {
		return fmt.Errorf("disk.grow exec: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("resize2fs failed: %s", strings.TrimSpace(result.Stderr))
	}
	return nil
}

// Stop terminates a running VM.
func (d *Driver) Stop(ctx context.Context, workspaceID string) error {
	d.mu.Lock()
	delete(d.readyTools, workspaceID)
	d.mu.Unlock()
	return d.manager.Stop(ctx, workspaceID)
}

// Pause is implemented as Stop for libkrun (no pause/resume support).
func (d *Driver) Pause(ctx context.Context, workspaceID string) error {
	return d.Stop(ctx, workspaceID)
}

// Resume re-registers the workspace so EnsureStarted will restart it on the next call.
// It clears the ready-tools cache so the next start probes the guest properly.
func (d *Driver) Resume(ctx context.Context, workspaceID string) error {
	d.mu.RLock()
	root := d.projectRoots[workspaceID]
	d.mu.RUnlock()
	if root == "" {
		return fmt.Errorf("workspace %s has no recorded project root", workspaceID)
	}
	d.mu.Lock()
	delete(d.readyTools, workspaceID)
	d.mu.Unlock()
	return d.EnsureStarted(ctx, workspaceID, root)
}

// ForkWithRoot registers the child workspace pointing at childRoot.
func (d *Driver) ForkWithRoot(_ context.Context, parentID, childID, childRoot string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.projectRoots[parentID]; !ok {
		return fmt.Errorf("parent workspace %s not found", parentID)
	}
	if _, ok := d.projectRoots[childID]; ok {
		return fmt.Errorf("workspace %s already exists", childID)
	}
	d.projectRoots[childID] = childRoot
	d.snapshotIDs[childID] = childID
	return nil
}

// CheckpointFork snapshots the parent's workspace overlay for the child.
// The parent VM is stopped before the copy to ensure filesystem consistency,
// then restarted afterward so the user's session is not permanently interrupted.
func (d *Driver) CheckpointFork(ctx context.Context, parentID, childID string) (string, error) {
	d.mu.RLock()
	parentRoot := d.projectRoots[parentID]
	d.mu.RUnlock()

	parentWasRunning := false
	if _, err := d.manager.Get(parentID); err == nil {
		parentWasRunning = true
		if stopErr := d.manager.Stop(ctx, parentID); stopErr != nil {
			log.Printf("[libkrun] CheckpointFork: stop parent %s: %v (continuing)", parentID, stopErr)
		}
	}

	if err := d.manager.ForkWorkspaceImage(ctx, parentID, childID, parentRoot); err != nil {
		if parentWasRunning && parentRoot != "" {
			_ = d.EnsureStarted(ctx, parentID, parentRoot)
		}
		return "", fmt.Errorf("fork workspace image: %w", err)
	}

	if parentWasRunning && parentRoot != "" {
		if restartErr := d.EnsureStarted(ctx, parentID, parentRoot); restartErr != nil {
			log.Printf("[libkrun] CheckpointFork: restart parent %s: %v", parentID, restartErr)
		}
	}
	return childID, nil
}

// Destroy stops the VM and removes all state.
func (d *Driver) Destroy(ctx context.Context, workspaceID string) error {
	d.mu.Lock()
	delete(d.projectRoots, workspaceID)
	delete(d.snapshotIDs, workspaceID)
	delete(d.readyTools, workspaceID)
	d.mu.Unlock()
	return d.manager.CleanupWorkspaceByID(ctx, workspaceID)
}

func defaultMemMiB() int {
	for _, key := range []string{"NEXUS_LIBKRUN_MEM_MIB", "NEXUS_VM_MEM_MIB"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				return n
			}
		}
	}
	return 4096
}
