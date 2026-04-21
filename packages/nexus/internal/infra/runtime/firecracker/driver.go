package firecracker

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

	"github.com/mdlayher/vsock"
)

// Driver interface local stub — TODO: replace with internal/domain/runtime.Driver once P2 lands.
type Driver interface {
	Backend() string
	Create(ctx context.Context, req CreateRequest) error
	Start(ctx context.Context, workspaceID string) error
	Stop(ctx context.Context, workspaceID string) error
	Pause(ctx context.Context, workspaceID string) error
	Resume(ctx context.Context, workspaceID string) error
	Fork(ctx context.Context, workspaceID, childWorkspaceID string) error
	Destroy(ctx context.Context, workspaceID string) error
}

var _ Driver = (*FCDriver)(nil)

// CommandRunner runs a shell command in a directory.
type CommandRunner interface {
	Run(ctx context.Context, dir string, cmd string, args ...string) error
}

// ManagerInterface defines the interface for VM lifecycle management.
type ManagerInterface interface {
	Spawn(ctx context.Context, spec SpawnSpec) (*Instance, error)
	Stop(ctx context.Context, workspaceID string) error
	CleanupWorkspaceByID(ctx context.Context, workspaceID string) error
	Get(workspaceID string) (*Instance, error)
	GrowWorkspace(ctx context.Context, workspaceID string, newSizeBytes int64) error
	CheckpointForkSnapshot(ctx context.Context, workspaceID, childWorkspaceID string) (string, error)
	ReconcileOrphans(ctx context.Context, liveWorkspaceIDs map[string]struct{}) error
}

// FCDriver implements the Firecracker runtime driver.
type FCDriver struct {
	runner       CommandRunner
	manager      ManagerInterface
	projectRoots map[string]string
	snapshotIDs  map[string]string // optional lineage snapshot ID per workspace
	agents       map[string]*AgentClient
	mu           sync.RWMutex
}

// Option is a functional option for FCDriver.
type Option func(*FCDriver)

// WithManager sets the VM manager.
func WithManager(manager ManagerInterface) Option {
	return func(d *FCDriver) {
		d.manager = manager
	}
}

// New creates a new Firecracker driver.
func New(runner CommandRunner, opts ...Option) *FCDriver {
	d := &FCDriver{
		runner:       runner,
		projectRoots: make(map[string]string),
		snapshotIDs:  make(map[string]string),
		agents:       make(map[string]*AgentClient),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Backend returns the driver backend name.
func (d *FCDriver) Backend() string {
	return "firecracker"
}

// CleanupStaleInstances removes stale/orphaned VM state on daemon startup.
func (d *FCDriver) CleanupStaleInstances(ctx context.Context) error {
	if d.manager == nil {
		return errors.New("manager is required for firecracker driver")
	}
	return d.manager.ReconcileOrphans(ctx, map[string]struct{}{})
}

// GuestWorkdir returns the in-guest working directory for a workspace.
func (d *FCDriver) GuestWorkdir(_ string) string {
	return "/workspace"
}

// ResolveWorkspaceEndpoint returns the guest-internal endpoint for a workspace service port.
func (d *FCDriver) ResolveWorkspaceEndpoint(_ context.Context, workspaceID string, remotePort int) (string, int, error) {
	if remotePort <= 0 {
		return "", 0, fmt.Errorf("remote port must be > 0")
	}
	if d.manager == nil {
		return "", 0, errors.New("manager is required for firecracker driver")
	}
	inst, err := d.manager.Get(workspaceID)
	if err != nil {
		return "", 0, fmt.Errorf("workspace instance lookup failed: %w", err)
	}
	if inst == nil || inst.CID == 0 {
		return "", 0, errors.New("workspace instance has no guest CID")
	}
	return guestIPForCID(inst.CID), remotePort, nil
}

func guestIPForCID(cid uint32) string {
	return fmt.Sprintf("172.26.%d.%d", byte((cid>>8)&0xFF), byte(cid&0xFF))
}

func (d *FCDriver) workspaceDir(workspaceID string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.projectRoots[workspaceID]
}

// AgentConn opens a vsock connection to the guest agent for the given workspace.
func (d *FCDriver) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	if d.manager == nil {
		return nil, errors.New("manager is required for firecracker driver")
	}

	inst, err := d.manager.Get(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace instance lookup failed: %w", err)
	}

	if inst == nil || inst.CID == 0 {
		return nil, errors.New("workspace instance has no guest CID")
	}

	if strings.TrimSpace(inst.VSockPath) != "" {
		waitUntil := time.Now().Add(10 * time.Second)
		if deadline, ok := ctx.Deadline(); ok && deadline.Before(waitUntil) {
			waitUntil = deadline
		}

		var lastErr error
		for time.Now().Before(waitUntil) {
			netDialer := &net.Dialer{Timeout: 2 * time.Second}
			conn, err := netDialer.DialContext(ctx, "unix", inst.VSockPath)
			if err == nil {
				if _, err := fmt.Fprintf(conn, "CONNECT %d\n", DefaultAgentVSockPort); err == nil {
					_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
					resp, readErr := bufio.NewReader(conn).ReadString('\n')
					_ = conn.SetReadDeadline(time.Time{})
					if readErr == nil && strings.HasPrefix(strings.TrimSpace(resp), "OK") {
						return conn, nil
					}
					if readErr != nil {
						lastErr = fmt.Errorf("connect handshake read: %w", readErr)
					} else {
						lastErr = fmt.Errorf("connect handshake rejected: %s", strings.TrimSpace(resp))
					}
				} else {
					lastErr = fmt.Errorf("connect handshake write: %w", err)
				}
				_ = conn.Close()
			} else {
				lastErr = err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(150 * time.Millisecond):
			}
		}
		if lastErr == nil {
			lastErr = errors.New("timeout waiting for agent vsock socket")
		}
		return nil, fmt.Errorf("agent unix-vsock connect failed (%s): %w", inst.VSockPath, lastErr)
	}

	conn, err := vsock.Dial(inst.CID, DefaultAgentVSockPort, nil)
	if err != nil {
		return nil, fmt.Errorf("vsock dial failed: %w", err)
	}

	return conn, nil
}

// Create registers a workspace lazily — it records the project root and optional
// snapshot ID but does NOT spawn the Firecracker VM yet. The VM is started on
// demand by EnsureStarted (called via workspace.start or pty.create). This keeps
// workspace.create fast: no mkfs.ext4, no VM boot.
func (d *FCDriver) Create(_ context.Context, req CreateRequest) error {
	if req.ProjectRoot == "" {
		return errors.New("project root is required")
	}

	d.mu.Lock()
	d.projectRoots[req.WorkspaceID] = req.ProjectRoot
	if req.Options != nil {
		if snap := strings.TrimSpace(req.Options["lineage_snapshot_id"]); snap != "" {
			d.snapshotIDs[req.WorkspaceID] = snap
		}
	}
	d.mu.Unlock()

	log.Printf("[firecracker] registered workspace %s (lazy; VM starts on first start)", req.WorkspaceID)
	return nil
}

func parsePositiveIntOption(options map[string]string, key string, fallback int) int {
	if options == nil {
		return fallback
	}
	raw := strings.TrimSpace(options[key])
	if raw == "" {
		return fallback
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val <= 0 {
		return fallback
	}
	return val
}

// GrowWorkspace grows the workspace backing image and notifies the guest.
func (d *FCDriver) GrowWorkspace(ctx context.Context, workspaceID string, newSizeBytes int64) error {
	if d.manager == nil {
		return errors.New("manager is required for firecracker driver")
	}

	if err := d.manager.GrowWorkspace(ctx, workspaceID, newSizeBytes); err != nil {
		return fmt.Errorf("host-side grow failed: %w", err)
	}

	conn, err := d.AgentConn(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("agent connect for disk.grow: %w", err)
	}
	defer conn.Close()

	client := NewAgentClient(conn)
	req := ExecRequest{
		ID:      fmt.Sprintf("disk-grow-%d", time.Now().UnixNano()),
		Type:    "disk.grow",
		Command: "",
	}
	result, err := client.Exec(ctx, req)
	if err != nil {
		return fmt.Errorf("disk.grow exec failed: %w", err)
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", result.ExitCode)
		}
		return fmt.Errorf("resize2fs failed: %s", detail)
	}

	return nil
}

// Start is a no-op: Firecracker VMs start immediately after Spawn.
func (d *FCDriver) Start(_ context.Context, _ string) error {
	return nil
}

// EnsureStarted guarantees there is a live Firecracker instance for workspaceID.
// This is the real "start" path: it runs mkfs.ext4 (if needed) and boots the VM.
// Called by Adapter.Start (workspace.start RPC) and AgentConn (pty.create RPC).
func (d *FCDriver) EnsureStarted(ctx context.Context, workspaceID, projectRoot string) error {
	if d.manager == nil {
		return errors.New("manager is required for firecracker driver")
	}

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
		return fmt.Errorf("workspace %s has no project root for firecracker start", workspaceID)
	}

	d.mu.RLock()
	snapshotID := d.snapshotIDs[workspaceID]
	d.mu.RUnlock()

	memMiB := defaultMemMiB()

	// Build a host config drive (ext4 image) so the guest can access gitconfig,
	// SSH material, and tool auth without any vsock file-transfer complexity.
	home, _ := os.UserHomeDir()
	configDrivePath := filepath.Join(root, ".nexus-host-config.ext4")
	if err := buildHostConfigDrive(home, configDrivePath); err != nil {
		log.Printf("[firecracker] warning: host config drive: %v", err)
		configDrivePath = ""
	}

	spec := SpawnSpec{
		WorkspaceID:     workspaceID,
		ProjectRoot:     root,
		MemoryMiB:       memMiB,
		VCPUs:           1,
		SnapshotID:      snapshotID,
		HostConfigDrive: configDrivePath,
	}

	inst, err := d.manager.Spawn(ctx, spec)
	if err != nil {
		return err
	}

	log.Printf("[firecracker] started workspace %s (cid=%d)", workspaceID, inst.CID)

	// Start host-side SSH agent vsock proxy (no private keys in the VM).
	startSSHAgentProxy(inst.WorkDir)

	return nil
}

// setupGuestEnvironment syncs the host's git config and SSH keys into the
// guest and configures safe.directory so git works against /workspace.

// defaultMemMiB returns the VM memory size in MiB, respecting NEXUS_FIRECRACKER_MEM_MIB.
func defaultMemMiB() int {
	if raw := strings.TrimSpace(os.Getenv("NEXUS_FIRECRACKER_MEM_MIB")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 1024
}

// Stop terminates a running VM.
func (d *FCDriver) Stop(ctx context.Context, workspaceID string) error {
	if d.manager == nil {
		return errors.New("manager is required for firecracker driver")
	}

	d.mu.Lock()
	delete(d.agents, workspaceID)
	d.mu.Unlock()

	return d.manager.Stop(ctx, workspaceID)
}

// Pause suspends a VM by stopping it.
func (d *FCDriver) Pause(ctx context.Context, workspaceID string) error {
	return d.Stop(ctx, workspaceID)
}

// Resume re-creates a previously stopped VM.
func (d *FCDriver) Resume(ctx context.Context, workspaceID string) error {
	if d.manager == nil {
		return errors.New("manager is required for firecracker driver")
	}

	d.mu.RLock()
	projectRoot := strings.TrimSpace(d.projectRoots[workspaceID])
	d.mu.RUnlock()
	if projectRoot == "" {
		return fmt.Errorf("workspace %s has no recorded project root", workspaceID)
	}

	err := d.Create(ctx, CreateRequest{
		WorkspaceID: workspaceID,
		ProjectRoot: projectRoot,
	})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// Fork records the parent root for a new child workspace ID.
func (d *FCDriver) Fork(_ context.Context, workspaceID, childWorkspaceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	parentProjectRoot := strings.TrimSpace(d.projectRoots[workspaceID])
	if parentProjectRoot == "" {
		return fmt.Errorf("parent workspace %s not found", workspaceID)
	}
	if _, exists := d.projectRoots[childWorkspaceID]; exists {
		return fmt.Errorf("workspace %s already exists", childWorkspaceID)
	}
	d.projectRoots[childWorkspaceID] = parentProjectRoot
	return nil
}

// CheckpointFork creates a copy-on-write fork snapshot of the parent workspace.
func (d *FCDriver) CheckpointFork(ctx context.Context, workspaceID, childWorkspaceID string) (string, error) {
	if d.manager == nil {
		return "", errors.New("manager is required for firecracker CheckpointFork")
	}
	snapshotID, err := d.manager.CheckpointForkSnapshot(ctx, workspaceID, childWorkspaceID)
	if err != nil {
		return "", fmt.Errorf("firecracker CheckpointFork: %w", err)
	}
	return snapshotID, nil
}

// Destroy stops the VM and removes all workspace state.
func (d *FCDriver) Destroy(ctx context.Context, workspaceID string) error {
	d.mu.Lock()
	delete(d.projectRoots, workspaceID)
	delete(d.agents, workspaceID)
	d.mu.Unlock()

	if d.manager != nil {
		if err := d.manager.CleanupWorkspaceByID(ctx, workspaceID); err != nil {
			log.Printf("[firecracker] warning: cleanup workspace %s: %v", workspaceID, err)
		}
	}

	return nil
}
