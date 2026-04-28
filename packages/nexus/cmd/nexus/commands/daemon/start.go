package daemon

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/oursky/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/oursky/nexus/packages/nexus/internal/daemon"
	"github.com/spf13/cobra"
)

// daemonForegroundEnv is set to "1" in the re-exec'd child process to signal
// it should skip setup and run the daemon in the foreground.
const daemonForegroundEnv = "NEXUS_DAEMON_FOREGROUND"

// StartSetupFn, if non-nil, is called once before the daemon starts to
// provision host prerequisites (libkrun libraries, kernel, rootfs).
// On Linux this is wired to RunRootlessBootstrap by the main package.
// driver is the runtime driver override ("libkrun", or "").
var StartSetupFn func(w io.Writer, driver string) error

// StartSetupFnJSON, if non-nil, is the JSON-output variant of StartSetupFn.
// Used when --json is passed to emit structured phase events.
var StartSetupFnJSON func(w io.Writer, driver string) error

// EmbeddedAgentFn, if non-nil, returns the embedded guest-agent binary bytes.
// On Linux builds this is always set by daemon_hooks_linux.go.
var EmbeddedAgentFn func() []byte

// LibkrunBakeFn, if non-nil, is called after agent injection to pre-bake
// developer tools into the base rootfs. Set on Linux by libkrun_bake_linux.go.
var LibkrunBakeFn func(libkrunVMBin, rootfsPath, kernelPath string, emit func(status, message string))

// DefaultVMKernelPath is the canonical kernel location after rootless daemon setup.
var DefaultVMKernelPath = defaultVMKernelPath()

// DefaultVMRootfsPath is the canonical rootfs location after rootless daemon setup.
var DefaultVMRootfsPath = defaultVMRootfsPath()

// resolvedPaths holds the final VM asset and workspace paths after promotion.
type resolvedPaths struct {
	rootfsPath  string
	kernelPath  string
	workDirRoot string
	basesDir    string
}

func startCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the Nexus daemon (auto-provisions prerequisites; listens on 127.0.0.1:7777 by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			emitPhase := makePhaseEmitter(cmd, jsonOutput)
			isForegroundChild := os.Getenv(daemonForegroundEnv) == "1"

			if err := runHostPrerequisites(cmd, isForegroundChild, driver); err != nil {
				return err
			}
			applyCanonicalPaths()

			token, err := resolveDaemonToken(cmd, network)
			if err != nil {
				return err
			}

			netCfg, err := buildNetworkConfig(token)
			if err != nil {
				return err
			}

			driverInfo, err := resolveDriver(driver, sandboxMode)
			if err != nil {
				return err
			}
			isLibkrun := driverInfo.Implementation == "libkrun"

			paths, err := resolveVMPaths(isLibkrun, rootfsPath, kernelPath, workDirRoot)
			if err != nil {
				return err
			}
			if err := validateVMAssets(sandboxMode, paths.rootfsPath, paths.kernelPath); err != nil {
				return err
			}
			setupLDLibraryPath(isLibkrun)

			cfg := daemon.Config{
				DBPath:          dbPath,
				SocketPath:      socketPath,
				KernelPath:      paths.kernelPath,
				RootFSPath:      paths.rootfsPath,
				WorkDirRoot:     paths.workDirRoot,
				BasesDir:        paths.basesDir,
				NodeName:        nodeName,
				Network:         netCfg,
				Driver:          driverInfo.Implementation,
				DriverCategory:  string(driverInfo.Category),
				EmbeddedAgentFn: EmbeddedAgentFn,
			}

			if err := injectAgentAndBake(cfg, isLibkrun, isForegroundChild, emitPhase); err != nil {
				return err
			}

			if !foreground && !isForegroundChild {
				return launchDaemonBackground(cmd.OutOrStdout(), socketPath, jsonOutput, readyTimeout)
			}

			return runDaemonForeground(cfg)
		},
	}

	registerStartFlags(cmd)
	return cmd
}

// makePhaseEmitter returns a structured-phase emitter bound to cmd and jsonOut.
func makePhaseEmitter(cmd *cobra.Command, jsonOut bool) func(phase, status, message string) {
	return func(phase, status, message string) {
		if strings.TrimSpace(phase) == "" {
			phase = "daemon-start"
		}
		if jsonOut {
			fmt.Fprintf(cmd.OutOrStdout(), `{"phase":%q,"status":%q,"message":%q}`+"\n", phase, status, message)
			return
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s: %s\n", phase, status, message)
	}
}

// runHostPrerequisites invokes StartSetupFn once before the daemon starts.
// It is skipped in the background child process since the parent already ran it.
func runHostPrerequisites(cmd *cobra.Command, isForegroundChild bool, driver string) error {
	if isForegroundChild || StartSetupFn == nil {
		return nil
	}
	setupOut := cmd.ErrOrStderr()
	setupFn := StartSetupFn
	if StartSetupFnJSON != nil && jsonOutput {
		setupOut = cmd.OutOrStdout()
		setupFn = StartSetupFnJSON
	}
	if err := setupFn(setupOut, driver); err != nil {
		return fmt.Errorf("daemon start: host setup: %w", err)
	}
	return nil
}

// applyCanonicalPaths sets default kernel and rootfs paths in production Linux
// builds where StartSetupFn has already provisioned the assets.
func applyCanonicalPaths() {
	if StartSetupFn == nil || runtime.GOOS == "darwin" {
		return
	}
	if kernelPath == "" {
		kernelPath = DefaultVMKernelPath
	}
	if rootfsPath == "" {
		rootfsPath = DefaultVMRootfsPath
	}
}

// resolveDaemonToken resolves the bearer token from flag > env > tokenstore.
func resolveDaemonToken(cmd *cobra.Command, network bool) (string, error) {
	if token != "" {
		return token, nil
	}
	if t := os.Getenv("NEXUS_DAEMON_TOKEN"); t != "" {
		return t, nil
	}
	if !network {
		return "", nil
	}
	t, err := tokenstore.LoadOrGenerate()
	if err != nil {
		return "", fmt.Errorf("daemon start: token: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "nexus: daemon token loaded from store\n")
	return t, nil
}

// buildNetworkConfig assembles and validates the network configuration.
func buildNetworkConfig(token string) (daemon.NetworkConfig, error) {
	cfg := daemon.NetworkConfig{
		Enabled:          network,
		BindAddress:      bind,
		Port:             port,
		TLSMode:          tlsMode,
		TokenAuthEnabled: token != "",
		Token:            token,
		TLSCertFile:      tlsCert,
		TLSKeyFile:       tlsKey,
	}
	if err := daemon.ValidateNetworkConfig(cfg); err != nil {
		return daemon.NetworkConfig{}, fmt.Errorf("daemon start: invalid network config: %w", err)
	}
	return cfg, nil
}

// resolveDriver parses the driver flag and applies compile-time defaults.
func resolveDriver(driver string, sandboxMode bool) (DriverInfo, error) {
	if driver == "" && defaultDriver != "" {
		driver = defaultDriver
		log.Printf("daemon: using compile-time default driver=%s", driver)
	}
	info, err := ParseDriver(driver)
	if err != nil {
		return DriverInfo{}, fmt.Errorf("daemon start: %w", err)
	}
	if info.Implementation == "" && sandboxMode {
		info = DriverInfo{Category: CategoryProcess, Implementation: "sandbox"}
	}
	return info, nil
}

// resolveVMPaths promotes VM assets to the preferred storage root when using
// libkrun and derives the bases and work directories.
func resolveVMPaths(isLibkrun bool, rootfsPath, kernelPath, workDirRoot string) (resolvedPaths, error) {
	defaultData := defaultDataDir()
	basesDir := filepath.Join(defaultData, "bases")

	if !isLibkrun {
		return resolvedPaths{
			rootfsPath:  rootfsPath,
			kernelPath:  kernelPath,
			workDirRoot: workDirRoot,
			basesDir:    basesDir,
		}, nil
	}

	storageRoot, ok := preferredLibkrunStorageRoot()
	if !ok {
		return resolvedPaths{
			rootfsPath:  rootfsPath,
			kernelPath:  kernelPath,
			workDirRoot: workDirRoot,
			basesDir:    basesDir,
		}, nil
	}

	basesDir = filepath.Join(storageRoot, "bases")

	if rootfsPath != "" {
		promotedRootfs, err := promoteVMAssetToDir(rootfsPath, filepath.Join(storageRoot, "vm"))
		if err != nil {
			return resolvedPaths{}, fmt.Errorf("daemon start: promote rootfs to %s: %w", storageRoot, err)
		}
		rootfsPath = promotedRootfs
	}
	if kernelPath != "" {
		promotedKernel, err := promoteVMAssetToDir(kernelPath, filepath.Join(storageRoot, "vm"))
		if err == nil {
			kernelPath = promotedKernel
		} else {
			log.Printf("daemon: kernel promote skipped (non-fatal): %v", err)
		}
	}
	if strings.TrimSpace(workDirRoot) == "" {
		workDirRoot = filepath.Join(storageRoot, "libkrun-vms")
	}
	log.Printf(
		"daemon: libkrun storage root=%s rootfs=%s bases=%s workdir=%s",
		storageRoot, rootfsPath, basesDir, workDirRoot,
	)

	return resolvedPaths{
		rootfsPath:  rootfsPath,
		kernelPath:  kernelPath,
		workDirRoot: workDirRoot,
		basesDir:    basesDir,
	}, nil
}

// validateVMAssets ensures rootfs and kernel exist when not in sandbox mode.
func validateVMAssets(sandboxMode bool, rootfsPath, kernelPath string) error {
	if sandboxMode {
		return nil
	}
	if rootfsPath == "" || kernelPath == "" {
		return fmt.Errorf(
			"daemon start: vm driver requires --rootfs and --kernel.\n" +
				"  Run `nexus daemon start` (auto-provisions assets) or supply the flags.",
		)
	}
	if _, err := os.Stat(rootfsPath); err != nil {
		return fmt.Errorf("daemon start: rootfs %q not found: %w\n  Run `nexus daemon start` first or supply --rootfs", rootfsPath, err)
	}
	if _, err := os.Stat(kernelPath); err != nil {
		return fmt.Errorf("daemon start: kernel %q not found: %w\n  Run `nexus daemon start` first or supply --kernel", kernelPath, err)
	}
	return nil
}

// setupLDLibraryPath ensures the shared libraries directory is on LD_LIBRARY_PATH
// so both the agent-injection step and the background daemon child can dlopen
// libkrun.so.1 without an RPATH that embeds the exact home dir.
func setupLDLibraryPath(isLibkrun bool) {
	if !isLibkrun || StartSetupFn == nil {
		return
	}
	home, _ := os.UserHomeDir()
	libDir := filepath.Join(home, ".local", "share", "nexus", "lib")
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		_ = os.Setenv("LD_LIBRARY_PATH", libDir+":"+existing)
	} else {
		_ = os.Setenv("LD_LIBRARY_PATH", libDir)
	}
}

// injectAgentAndBake refreshes the guest-agent binary in the rootfs and, in
// non-CI environments, pre-bakes developer tools into the base rootfs.
func injectAgentAndBake(cfg daemon.Config, isLibkrun, isForegroundChild bool, emitPhase func(phase, status, message string)) error {
	if sandboxMode || isForegroundChild {
		return nil
	}
	if err := ensureGuestAgent(cfg.RootFSPath); err != nil {
		return fmt.Errorf("daemon start: guest agent refresh: %w", err)
	}

	skipLibkrunBake := isSkipLibkrunBake()
	if isLibkrun && LibkrunBakeFn != nil && !skipLibkrunBake {
		vmBinDir := resolveVMBinDir()
		LibkrunBakeFn(filepath.Join(vmBinDir, "nexus-libkrun-vm"), cfg.RootFSPath, cfg.KernelPath, func(status, message string) {
			emitPhase("rootfs-bake", status, message)
		})
	} else if isLibkrun && skipLibkrunBake {
		reason := skipBakeReason()
		emitPhase("rootfs-bake", "ok", "skipped ("+reason+")")
	}
	return nil
}

// isSkipLibkrunBake returns true when the environment requests skipping the
// libkrun rootfs bake step.
func isSkipLibkrunBake() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_SKIP_BAKE")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_SKIP_BAKE")), "true") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "true")
}

// resolveVMBinDir returns the directory containing the nexus-libkrun-vm binary.
func resolveVMBinDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "nexus", "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus", "bin")
}

// skipBakeReason returns a human-readable reason for skipping the bake.
func skipBakeReason() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "true") {
		return "CI environment"
	}
	return "NEXUS_LIBKRUN_SKIP_BAKE set"
}

// runDaemonForeground creates, starts, and blocks on the daemon until signal.
func runDaemonForeground(cfg daemon.Config) error {
	d, err := daemon.New(cfg)
	if err != nil {
		return fmt.Errorf("daemon start: init: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := d.Start(ctx); err != nil && err != context.Canceled {
		log.Printf("daemon: stopped: %v", err)
	}
	if err := d.Stop(); err != nil {
		log.Printf("daemon: stop: %v", err)
	}
	return nil
}
