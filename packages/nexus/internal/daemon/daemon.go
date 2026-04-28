// Package daemon is the composition root for the Nexus daemon.
package daemon

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	apppty "github.com/oursky/nexus/packages/nexus/internal/app/pty"
	appspotlight "github.com/oursky/nexus/packages/nexus/internal/app/spotlight"
	appworkspace "github.com/oursky/nexus/packages/nexus/internal/app/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/creds/relay"
	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/dockercompose"
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
	DBPath      string
	SocketPath  string
	KernelPath  string
	RootFSPath  string
	WorkDirRoot string
	BasesDir    string
	NodeName    string
	NodeTags    []string
	Network     NetworkConfig
	// Driver selects the runtime driver implementation: "libkrun" or "sandbox".
	Driver string
	// DriverCategory is the user-facing category: "vm" or "process".
	DriverCategory string
	// EmbeddedAgentFn returns the embedded guest-agent binary bytes.
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

	// Build runtime registry with all available drivers.
	registry := domainruntime.NewRegistry()

	// Sandbox (process) driver is always available.
	sandboxDriver := sandbox.NewAdapter(sandbox.NewDriver())
	registry.Register(sandboxDriver)
	log.Printf("daemon: sandbox (process) runtime driver registered")

	// Attempt to register libkrun driver on all platforms (stub on non-Linux).
	var lkBundle libkrunDriverBundle
	var lkErr error
	lkBundle, lkErr = buildLibkrunDriver(cfg)
	if lkErr == nil {
		if err := lkBundle.CleanupStaleInstances(context.Background()); err != nil {
			log.Printf("daemon: libkrun stale cleanup warning: %v", err)
		}
		registry.Register(lkBundle.AsDriver())
		log.Printf("daemon: libkrun runtime driver registered")

		// Pre-warm base workspace images for all known libkrun workspaces in
		// the background so the first workspace start doesn't block on
		// mkfs.ext4 -d <project_root> (which can take 30-120s for large repos).
		go prewarmLibkrunBaseImages(wsStore, cfg.BasesDir)
	} else {
		log.Printf("daemon: libkrun driver not available: %v", lkErr)
	}

	// Determine default backend: explicit driver flag > platform default.
	switch cfg.Driver {
	case "libkrun":
		if !registry.SetDefaultBackend("libkrun") {
			db.Close()
			return nil, fmt.Errorf("daemon: requested driver %q is not available on this node", cfg.Driver)
		}
	case "sandbox", "process":
		registry.SetDefaultBackend("sandbox")
	default:
		if runtime.GOOS == "linux" && registry.HasBackend("libkrun") {
			registry.SetDefaultBackend("libkrun")
		} else {
			registry.SetDefaultBackend("sandbox")
		}
	}
	log.Printf("daemon: default backend=%s", registry.DefaultBackend())

	// Reconcile workspace states for all backends that need it.
	reconcileWorkspaceStates(context.Background(), wsStore)

	// ── Workspace handler with optional serial log provider ────────────────
	wsHandlerOpts := []rpcworkspace.HandlerOption{}
	if registry.HasBackend("libkrun") {
		wsHandlerOpts = append(wsHandlerOpts, rpcworkspace.WithSerialLogProvider(lkBundle))
	}

	spotlightOpts := []appspotlight.Option{}
	if registry.HasBackend("libkrun") {
		spotlightOpts = append(spotlightOpts, appspotlight.WithPortDialer(lkBundle))
	}
	spotlightSvc := appspotlight.New(fwdStore, wsStore, spotlightOpts...)

	wsSvc := appworkspace.NewService(wsStore, projStore, registry, dockercomposePortDiscoverer{}, spotlightSvc, context.Background())
	ptyReg := apppty.NewRegistry()
	broker := relay.NewBroker()

	reg := rpcregistry.NewMapRegistry()

	rpcworkspace.NewHandler(wsSvc, wsHandlerOpts...).Register(reg)
	if err := rpcspotlight.New(spotlightSvc).Register(reg); err != nil {
		db.Close()
		return nil, fmt.Errorf("register spotlight rpc: %w", err)
	}
	ptyHandlerOpts := []rpcpty.HandlerOption{
		rpcpty.WithWorkspaceRepo(wsStore),
		rpcpty.WithProjectRepo(projStore),
	}
	if registry.HasBackend("libkrun") {
		ptyHandlerOpts = append(ptyHandlerOpts, rpcpty.WithVsockDialer(lkBundle))
		ptyHandlerOpts = append(ptyHandlerOpts, rpcpty.WithWorkspaceReadyChecker(lkBundle))
	}
	rpcpty.New(ptyReg, ptyHandlerOpts...).Register(reg)
	rpcfs.New("/").Register(reg)
	daemonLogPath := filepath.Join(filepath.Dir(cfg.SocketPath), "daemon.log")
	rpcdaemon.New(newNodeInfo(cfg, registry), rpcdaemon.WithLogPath(daemonLogPath)).Register(reg)
	rpcproject.New(projStore, rpcproject.WithWorkspaceRepo(wsStore)).Register(reg)
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

// reconcileWorkspaceStates resets stale workspace states after daemon restart.
// Both VM and process workspaces lose their runtime when the daemon restarts.
func reconcileWorkspaceStates(ctx context.Context, wsStore *store.WorkspaceStore) {
	all, err := wsStore.List(ctx)
	if err != nil {
		log.Printf("daemon: workspace state reconcile skipped: %v", err)
		return
	}
	for _, ws := range all {
		if ws == nil {
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
type nodeInfo struct {
	cfg      Config
	registry *domainruntime.Registry
}

func newNodeInfo(cfg Config, registry *domainruntime.Registry) *nodeInfo {
	return &nodeInfo{cfg: cfg, registry: registry}
}

func (n *nodeInfo) NodeName() string   { return n.cfg.NodeName }
func (n *nodeInfo) NodeTags() []string { return n.cfg.NodeTags }
func (n *nodeInfo) Capabilities() []rpcdaemon.Capability {
	if n.registry == nil {
		return []rpcdaemon.Capability{
			{Name: "runtime.process", Available: true},
			{Name: "runtime.libkrun", Available: false},
		}
	}
	caps := make([]rpcdaemon.Capability, 0, len(n.registry.Backends()))
	for _, backend := range n.registry.Backends() {
		caps = append(caps, rpcdaemon.Capability{Name: "runtime." + backend, Available: true})
	}
	return caps
}

// dockercomposePortDiscoverer adapts the dockercompose package to satisfy
// workspace.PortDiscoverer without leaking the concrete package into app/workspace.
type dockercomposePortDiscoverer struct{}

func (dockercomposePortDiscoverer) DiscoverPublishedPorts(ctx context.Context, repoRoot string) ([]domainws.DiscoveredPort, error) {
	ports, err := dockercompose.DiscoverPublishedPorts(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	result := make([]domainws.DiscoveredPort, len(ports))
	for i, p := range ports {
		result[i] = domainws.DiscoveredPort{
			LocalPort:  p.HostPort,
			RemotePort: p.HostPort,
			Service:    p.Service,
			Protocol:   p.Protocol,
			Source:     "compose",
		}
	}
	return result, nil
}
