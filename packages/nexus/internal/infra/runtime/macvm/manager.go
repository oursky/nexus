//go:build darwin

package macvm

import "sync"

// ManagerConfig configures the macOS VM manager.
type ManagerConfig struct {
	// LibDir is the directory containing libkrun.dylib and libkrunfw.dylib.
	// Defaults to ~/.cache/nexus/lib/.
	LibDir string
	// VMWorkDir is the root directory for per-workspace VM state (rootfs copies).
	// Defaults to ~/.cache/nexus/macvm-workspaces/.
	VMWorkDir string
	// RootFSCachePath is the path to the base rootfs.ext4 image.
	// Defaults to ~/.cache/nexus/vm/rootfs.ext4.
	RootFSCachePath string
	// RootFSDownloadURL is the URL to download the base rootfs if missing.
	RootFSDownloadURL string
	// EmbeddedAgentFn returns the embedded guest-agent binary bytes.
	EmbeddedAgentFn func() []byte
}

// vmInstance tracks a running macOS VM.
type vmInstance struct {
	workspaceID  string
	workDir      string // ~/.cache/nexus/macvm-workspaces/<wsID>/
	configDir    string // virtio-fs host config tmpdir (/run/nexus-host); removed on stop
	guestSSHPort int    // host port forwarded to guest :22
	agentPort    int    // host port forwarded to guest nexus-agent (TCP gvproxy fallback)
	pid          int    // libkrun-vm subprocess PID
	stop         func() // tear down gvproxy / VM goroutine
	done         <-chan struct{}
}

// Manager manages macOS libkrun VM instances.
type Manager struct {
	cfg         ManagerConfig
	vms         sync.Map // workspaceID → *vmInstance
	readyAgents sync.Map // workspaceID marked after successful WorkspaceReady (/bin/true) probe
}

// NewManager creates a Manager with the given config.
func NewManager(cfg ManagerConfig) *Manager {
	cfg = applyConfigDefaults(cfg)
	return &Manager{cfg: cfg}
}
