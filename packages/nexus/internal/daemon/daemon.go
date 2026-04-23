// Package daemon is the composition root for the Nexus daemon.
package daemon

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	apppty "github.com/oursky/nexus/packages/nexus/internal/app/pty"
	appspotlight "github.com/oursky/nexus/packages/nexus/internal/app/spotlight"
	appworkspace "github.com/oursky/nexus/packages/nexus/internal/app/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/creds/relay"
	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/firecracker"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/sandbox"
	"github.com/oursky/nexus/packages/nexus/internal/infra/store"
	rpcauth "github.com/oursky/nexus/packages/nexus/internal/rpc/auth"
	rpcdaemon "github.com/oursky/nexus/packages/nexus/internal/rpc/daemon"
	rpcfs "github.com/oursky/nexus/packages/nexus/internal/rpc/fs"
	rpcproject "github.com/oursky/nexus/packages/nexus/internal/rpc/project"
	rpcpty "github.com/oursky/nexus/packages/nexus/internal/rpc/pty"
	rpcregistry "github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
	rpcspotlight "github.com/oursky/nexus/packages/nexus/internal/rpc/spotlight"
	rpcworkspace "github.com/oursky/nexus/packages/nexus/internal/rpc/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/transport"
)

// NetworkConfig holds optional remote-listener settings.
type NetworkConfig struct {
	Enabled          bool
	BindAddress      string
	Port             int
	TLSMode          string // "off" | "auto" | "required"
	TokenAuthEnabled bool
	Token            string
	TLSCertFile      string
	TLSKeyFile       string
}

// ValidateNetworkConfig enforces policy rules for remote listener configuration.
func ValidateNetworkConfig(cfg NetworkConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if !cfg.TokenAuthEnabled {
		return fmt.Errorf("network listener requires token auth (--token must be set when --network is enabled)")
	}
	isLoopback := cfg.BindAddress == "127.0.0.1" || cfg.BindAddress == "::1"
	if !isLoopback && cfg.TLSMode == "off" {
		return fmt.Errorf("tls-mode \"off\" is not allowed for non-loopback bind address %q; use \"auto\" or \"required\"", cfg.BindAddress)
	}
	if len(cfg.Token) < 16 {
		log.Printf("daemon: WARNING: network auth token is fewer than 16 characters; use a longer token in production")
	}
	return nil
}

// Config holds all configuration for building a Daemon.
type Config struct {
	DBPath             string
	SocketPath         string
	FirecrackerEnabled bool
	FirecrackerBin     string
	KernelPath         string
	RootFSPath         string
	WorkDirRoot        string
	BasesDir           string
	NodeName           string
	NodeTags           []string
	Network            NetworkConfig
	// Driver overrides the runtime driver: "" or "firecracker" = Firecracker,
	// "libkrun" = libkrun (requires build tag libkrun), "sandbox" = process sandbox.
	Driver string
	// EmbeddedAgentFn returns the embedded nexus-firecracker-agent binary bytes.
	// When set, the libkrun driver injects the current agent into each VM's rootfs
	// on spawn so the agent stays in sync with the nexus binary.
	EmbeddedAgentFn func() []byte
}

// Daemon is the assembled daemon with all services wired together.
type Daemon struct {
	cfg             Config
	db              *store.DB
	registry        *rpcregistry.MapRegistry
	listener        *transport.Listener
	networkListener *transport.NetworkListener
}

// New constructs a Daemon by wiring all layers.
func New(cfg Config) (*Daemon, error) {
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(defaultDataDir(), "nexus.db")
	}
	if cfg.SocketPath == "" {
		cfg.SocketPath = filepath.Join(defaultDataDir(), "nexusd.sock")
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	wsStore := store.NewWorkspaceStore(db)
	projStore := store.NewProjectStore(db)
	fwdStore := store.NewForwardStore(db)

	var rtDriver domainruntime.Driver
	var fcDriver *firecracker.FCDriver

	var lkBundle libkrunDriverBundle

	switch {
	case cfg.Driver == "libkrun":
		// libkrun driver — built only when the "libkrun" build tag is set.
		var err error
		lkBundle, err = buildLibkrunDriver(cfg)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("daemon: libkrun driver: %w", err)
		}
		if err := lkBundle.CleanupStaleInstances(context.Background()); err != nil {
			log.Printf("daemon: libkrun stale cleanup warning: %v", err)
		}
		reconcileLibkrunWorkspaceStates(context.Background(), wsStore)
		rtDriver = lkBundle.AsDriver()
		log.Printf("daemon: libkrun runtime driver wired")

		// Pre-warm base workspace images for all known libkrun workspaces in
		// the background so the first workspace start doesn't block on
		// mkfs.ext4 -d <project_root> (which can take 30-120s for large repos).
		go prewarmLibkrunBaseImages(wsStore, cfg.BasesDir)

	case cfg.FirecrackerEnabled && cfg.Driver != "sandbox":
		if err := validateFirecrackerHostRouting(); err != nil {
			db.Close()
			return nil, err
		}
		var fcErr error
		fcDriver, fcErr = buildFirecrackerDriver(cfg)
		if fcErr != nil {
			db.Close()
			return nil, fmt.Errorf("firecracker: %w", fcErr)
		}
		if err := fcDriver.CleanupStaleInstances(context.Background()); err != nil {
			log.Printf("daemon: firecracker stale cleanup warning: %v", err)
		}
		reconcileFirecrackerWorkspaceStates(context.Background(), wsStore)
		rtDriver = firecracker.NewAdapter(fcDriver)
		log.Printf("daemon: firecracker runtime driver wired")

	default:
		rtDriver = sandbox.NewAdapter(sandbox.NewDriver())
		log.Printf("daemon: sandbox (process) runtime driver wired")
	}

	wsSvc := appworkspace.NewService(wsStore, projStore, rtDriver, context.Background())

	spotlightOpts := []appspotlight.Option{}
	if fcDriver != nil {
		spotlightOpts = append(spotlightOpts, appspotlight.WithPortDialer(fcDriver))
	} else if cfg.Driver == "libkrun" {
		spotlightOpts = append(spotlightOpts, appspotlight.WithPortDialer(lkBundle))
	}
	spotlightSvc := appspotlight.New(fwdStore, wsStore, spotlightOpts...)
	ptyReg := apppty.NewRegistry()
	broker := relay.NewBroker()

	reg := rpcregistry.NewMapRegistry()

	rpcworkspace.NewHandler(wsSvc).Register(reg)
	if err := rpcspotlight.New(spotlightSvc).Register(reg); err != nil {
		db.Close()
		return nil, fmt.Errorf("register spotlight rpc: %w", err)
	}
	ptyHandlerOpts := []rpcpty.HandlerOption{
		rpcpty.WithWorkspaceRepo(wsStore),
		rpcpty.WithProjectRepo(projStore),
	}
	if fcDriver != nil {
		ptyHandlerOpts = append(ptyHandlerOpts, rpcpty.WithVsockDialer(fcDriver))
		ptyHandlerOpts = append(ptyHandlerOpts, rpcpty.WithWorkspaceReadyChecker(fcDriver))
	} else if cfg.Driver == "libkrun" {
		ptyHandlerOpts = append(ptyHandlerOpts, rpcpty.WithVsockDialer(lkBundle))
		ptyHandlerOpts = append(ptyHandlerOpts, rpcpty.WithWorkspaceReadyChecker(lkBundle))
	}
	rpcpty.New(ptyReg, ptyHandlerOpts...).Register(reg)
	rpcfs.New("/").Register(reg)
	rpcdaemon.New(newNodeInfo(cfg)).Register(reg)
	rpcproject.New(projStore).Register(reg)
	rpcauth.New(wsStore, broker).Register(reg)

	lst := transport.NewListener(cfg.SocketPath, reg)

	d := &Daemon{
		cfg:      cfg,
		db:       db,
		registry: reg,
		listener: lst,
	}

	if cfg.Network.Enabled {
		nl, err := transport.NewNetworkListener(transport.NetworkListenerConfig{
			BindAddress: cfg.Network.BindAddress,
			Port:        cfg.Network.Port,
			TLSMode:     cfg.Network.TLSMode,
			Token:       cfg.Network.Token,
			TLSCertFile: cfg.Network.TLSCertFile,
			TLSKeyFile:  cfg.Network.TLSKeyFile,
		}, reg)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("network listener: %w", err)
		}
		d.networkListener = nl
	}

	return d, nil
}

// reconcileLibkrunWorkspaceStates resets stale libkrun workspace states after restart.
func reconcileLibkrunWorkspaceStates(ctx context.Context, wsStore *store.WorkspaceStore) {
	all, err := wsStore.List(ctx)
	if err != nil {
		log.Printf("daemon: libkrun workspace state reconcile skipped: %v", err)
		return
	}
	for _, ws := range all {
		if ws == nil || ws.Backend != "libkrun" {
			continue
		}
		switch ws.State {
		case domainws.StateRunning:
			ws.State = domainws.StateStopped
		case domainws.StateStarting:
			ws.State = domainws.StateCreated
		default:
			continue
		}
		ws.UpdatedAt = time.Now().UTC()
		if err := wsStore.Update(ctx, ws); err != nil {
			log.Printf("daemon: libkrun workspace state reconcile %s: %v", ws.ID, err)
		}
	}
}

// reconcileFirecrackerWorkspaceStates prevents stale DB state after daemon
// restarts: if runtime instances are cleaned up, mark previously "running" or
// "starting" Firecracker workspaces as stopped/created so callers trigger a
// real start path.
func reconcileFirecrackerWorkspaceStates(ctx context.Context, wsStore *store.WorkspaceStore) {
	all, err := wsStore.List(ctx)
	if err != nil {
		log.Printf("daemon: workspace state reconcile skipped: %v", err)
		return
	}
	for _, ws := range all {
		if ws == nil {
			continue
		}
		if ws.Backend != "firecracker" {
			continue
		}
		switch ws.State {
		case domainws.StateRunning:
			ws.State = domainws.StateStopped
		case domainws.StateStarting:
			// Start was interrupted mid-flight; reset so user can retry.
			ws.State = domainws.StateCreated
		default:
			continue
		}
		ws.UpdatedAt = time.Now().UTC()
		if err := wsStore.Update(ctx, ws); err != nil {
			log.Printf("daemon: workspace state reconcile %s: %v", ws.ID, err)
		}
	}
}

// Start begins accepting connections. It blocks until ctx is cancelled or an error occurs.
func (d *Daemon) Start(ctx context.Context) error {
	slog.Info("daemon.starting", "addr", d.cfg.SocketPath)
	log.Printf("daemon: listening on %s", d.cfg.SocketPath)
	slog.Info("daemon.ready", "addr", d.cfg.SocketPath)

	if d.networkListener == nil {
		return d.listener.Serve(ctx)
	}

	errCh := make(chan error, 2)
	go func() { errCh <- d.listener.Serve(ctx) }()
	go func() { errCh <- d.networkListener.Serve(ctx) }()

	err := <-errCh
	return err
}

// Stop shuts down the daemon, closing transport and database.
func (d *Daemon) Stop() error {
	var errs []error
	if err := d.listener.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close listener: %w", err))
	}
	if d.networkListener != nil {
		if err := d.networkListener.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close network listener: %w", err))
		}
	}
	if err := d.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close db: %w", err))
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func buildFirecrackerDriver(cfg Config) (*firecracker.FCDriver, error) {
	// Both firecracker-vms and firecracker-bases must live on the same XFS
	// volume so that cp --reflink=always works across them.
	dataDir := nexusDataVolumeDir()
	fsType := firecracker.FilesystemType(dataDir)
	if !firecracker.ReflinkAvailable(dataDir) {
		return nil, fmt.Errorf(
			"data volume at %s is %q — nexus requires XFS for reflink workspace clones.\n"+
				"See the Linux host prerequisites in the README, or set NEXUS_DATA_DIR to an XFS path.",
			dataDir, fsType,
		)
	}
	log.Printf("daemon: data volume %s (%s) — XFS reflink mode (O(1) workspace clones)", dataDir, fsType)

	workDirRoot := cfg.WorkDirRoot
	if workDirRoot == "" {
		workDirRoot = filepath.Join(dataDir, "firecracker-vms")
	}
	basesDir := filepath.Join(dataDir, "firecracker-bases")

	mgr := firecracker.NewManager(firecracker.ManagerConfig{
		FirecrackerBin: orDefault(cfg.FirecrackerBin, "firecracker"),
		KernelPath:     orDefault(cfg.KernelPath, os.Getenv("NEXUS_FIRECRACKER_KERNEL")),
		RootFSPath:     orDefault(cfg.RootFSPath, os.Getenv("NEXUS_FIRECRACKER_ROOTFS")),
		WorkDirRoot:    workDirRoot,
		BasesDir:       basesDir,
	})
	return firecracker.New(execRunner{}, firecracker.WithManager(mgr), firecracker.WithBasesDir(basesDir)), nil
}

// nexusDataVolumeDir returns the directory used for Firecracker VM state and
// base image cache. The directory must be on XFS for reflink support.
//
// Override via NEXUS_DATA_DIR env var or by mounting a volume at /data/nexus.
func nexusDataVolumeDir() string {
	if v := strings.TrimSpace(os.Getenv("NEXUS_DATA_DIR")); v != "" {
		return v
	}
	const dedicated = "/data/nexus"
	if fi, err := os.Stat(dedicated); err == nil && fi.IsDir() {
		if f, err := os.CreateTemp(dedicated, ".nexus-probe-*"); err == nil {
			f.Close()
			os.Remove(f.Name())
			return dedicated
		}
	}
	return defaultDataDir()
}

// loadFirecrackerBridgeSubnet returns the bridge subnet CIDR.
// Priority: NEXUS_BRIDGE_SUBNET env var → persisted file → default.
func loadFirecrackerBridgeSubnet() string {
	if s := os.Getenv("NEXUS_BRIDGE_SUBNET"); s != "" {
		return s
	}
	data, err := os.ReadFile("/var/lib/nexus/bridge-subnet")
	if err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return s
		}
	}
	return "172.26.0.0/16"
}

func validateFirecrackerHostRouting() error {
	const bridge = "nexusbr0"
	subnet := loadFirecrackerBridgeSubnet()
	out, err := exec.Command("ip", "-4", "route", "show", subnet).CombinedOutput()
	if err != nil {
		return fmt.Errorf("firecracker host route check failed for %s: %w", subnet, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] != "dev" {
				continue
			}
			dev := fields[i+1]
			if dev == bridge {
				break
			}
			return fmt.Errorf(
				"firecracker subnet conflict: %s route is owned by %q (expected %s)\n"+
					"Docker likely allocated %s for one of its project networks\n"+
					"to fix: run `sudo nexus init --project-root <path> --force` to configure\n"+
					"Docker address pools to exclude %s, then remove conflicting networks:\n"+
					"  docker network ls  # find networks with subnet %s\n"+
					"  docker network rm <id>",
				subnet, dev, bridge, subnet, subnet, subnet,
			)
		}
	}
	return nil
}

// execRunner satisfies firecracker.CommandRunner using os/exec.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, cmd string, args ...string) error {
	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = dir
	return c.Run()
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/var/lib/nexus"
	}
	return filepath.Join(home, ".local", "state", "nexus")
}

// nodeInfo wraps Config to satisfy rpcdaemon.NodeInfoProvider.
type nodeInfo struct{ cfg Config }

func newNodeInfo(cfg Config) *nodeInfo { return &nodeInfo{cfg: cfg} }

func (n *nodeInfo) NodeName() string   { return n.cfg.NodeName }
func (n *nodeInfo) NodeTags() []string { return n.cfg.NodeTags }
func (n *nodeInfo) Capabilities() []rpcdaemon.Capability {
	return []rpcdaemon.Capability{
		{Name: "runtime.process", Available: true},
		{Name: "runtime.firecracker", Available: n.cfg.FirecrackerEnabled},
		{Name: "runtime.libkrun", Available: n.cfg.Driver == "libkrun"},
	}
}
