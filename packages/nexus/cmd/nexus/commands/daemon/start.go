package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

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
var LibkrunBakeFn func(rootfsPath, kernelPath string, emit func(status, message string))

// DefaultVMKernelPath is the canonical kernel location after rootless daemon setup.
var DefaultVMKernelPath = defaultVMKernelPath()

// DefaultVMRootfsPath is the canonical rootfs location after rootless daemon setup.
var DefaultVMRootfsPath = defaultVMRootfsPath()

// DefaultVMRootfsDirPath is the canonical directory-form rootfs location used
// by libkrun set_root+set_exec launch mode.
var DefaultVMRootfsDirPath = defaultVMRootfsDirPath()

// defaultVMKernelPath returns the user-scoped kernel path under XDG_DATA_HOME.
func defaultVMKernelPath() string {
	return xdgVMAsset("vmlinux.bin")
}

// defaultVMRootfsPath returns the user-scoped rootfs path under XDG_DATA_HOME.
func defaultVMRootfsPath() string {
	return xdgVMAsset("rootfs.ext4")
}

// defaultVMRootfsDirPath returns the user-scoped rootfs directory path under
// XDG_DATA_HOME.
func defaultVMRootfsDirPath() string {
	return xdgVMAsset("rootfs-dir")
}

// xdgVMAsset returns the path to a VM asset under ~/.local/share/nexus/vm/
// or the legacy /var/lib/nexus/ path if the legacy file still exists and the
// XDG file does not, providing seamless migration for existing setups.
func xdgVMAsset(name string) string {
	home, _ := os.UserHomeDir()
	xdgPath := filepath.Join(home, ".local", "share", "nexus", "vm", name)
	legacyPath := "/var/lib/nexus/" + name

	if _, err := os.Stat(xdgPath); err == nil {
		return xdgPath
	}
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath
	}
	return xdgPath
}

func startCommand() *cobra.Command {
	var (
		dbPath       string
		socketPath   string
		kernelPath   string
		rootfsPath   string
		workDirRoot  string
		nodeName     string
		network      bool
		bind         string
		port         int
		tlsMode      string
		token        string
		tlsCert      string
		tlsKey       string
		foreground   bool   // --foreground: stay blocking instead of self-daemonizing
		sandboxMode  bool   // internal: use process sandbox backend
		jsonOutput   bool   // --json: emit structured phase events (rootless bootstrap)
		driver       string // --driver: runtime driver override (libkrun, sandbox)
		readyTimeout time.Duration
	)

	defaultData := defaultDataDir()

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the Nexus daemon (auto-provisions prerequisites; listens on 127.0.0.1:7777 by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			emitPhase := func(phase, status, message string) {
				if strings.TrimSpace(phase) == "" {
					phase = "daemon-start"
				}
				if jsonOutput {
					fmt.Fprintf(cmd.OutOrStdout(), `{"phase":%q,"status":%q,"message":%q}`+"\n", phase, status, message)
					return
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s: %s\n", phase, status, message)
			}

			isForegroundChild := os.Getenv(daemonForegroundEnv) == "1"

			// Auto-provision host prerequisites (libkrun libraries, kernel, rootfs).
			// Skipped in the background child process since the parent already ran it.
			if !isForegroundChild && StartSetupFn != nil {
				setupOut := cmd.ErrOrStderr()
				if StartSetupFnJSON != nil && jsonOutput {
					setupOut = cmd.OutOrStdout()
				}
				setupFn := StartSetupFn
				if StartSetupFnJSON != nil && jsonOutput {
					setupFn = StartSetupFnJSON
				}
				if err := setupFn(setupOut, driver); err != nil {
					return fmt.Errorf("daemon start: host setup: %w", err)
				}
			}

			// Apply canonical default paths only in production Linux builds
			// where StartSetupFn has already provisioned the assets.
			if StartSetupFn != nil && runtime.GOOS != "darwin" {
				if kernelPath == "" {
					kernelPath = DefaultVMKernelPath
				}
				if rootfsPath == "" {
					rootfsPath = DefaultVMRootfsPath
				}
			}

			// Resolve token: flag > env > tokenstore
			if token == "" {
				token = os.Getenv("NEXUS_DAEMON_TOKEN")
			}
			if token == "" && network {
				var err error
				token, err = tokenstore.LoadOrGenerate()
				if err != nil {
					return fmt.Errorf("daemon start: token: %w", err)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "nexus: daemon token loaded from store\n")
			}

			netCfg := daemon.NetworkConfig{
				Enabled:          network,
				BindAddress:      bind,
				Port:             port,
				TLSMode:          tlsMode,
				TokenAuthEnabled: token != "",
				Token:            token,
				TLSCertFile:      tlsCert,
				TLSKeyFile:       tlsKey,
			}
			if err := daemon.ValidateNetworkConfig(netCfg); err != nil {
				return fmt.Errorf("daemon start: invalid network config: %w", err)
			}

			// ── Driver selection ────────────────────────────────────────────────
			// Apply the compile-time default when --driver is not specified.
			if driver == "" && defaultDriver != "" {
				driver = defaultDriver
				log.Printf("daemon: using compile-time default driver=%s", driver)
			}

			isLibkrun := driver == "libkrun"
			basesDir := filepath.Join(defaultData, "bases")

			// Prefer colocating libkrun source images and workspace clones on /data/nexus
			// so cp --reflink can be used (same-device CoW clone).
			if isLibkrun {
				if storageRoot, ok := preferredLibkrunStorageRoot(); ok {
					basesDir = filepath.Join(storageRoot, "bases")
					if rootfsPath != "" {
						promotedRootfs, err := promoteVMAssetToDir(rootfsPath, filepath.Join(storageRoot, "vm"))
						if err != nil {
							return fmt.Errorf("daemon start: promote rootfs to %s: %w", storageRoot, err)
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
				}
			}

			// For libkrun, ensure the shared libraries directory is on LD_LIBRARY_PATH
			// so both the agent-injection step and the background daemon child can
			// dlopen libkrun.so.1 without an RPATH that embeds the exact home dir.
			if isLibkrun && StartSetupFn != nil {
				home, _ := os.UserHomeDir()
				libDir := filepath.Join(home, ".local", "share", "nexus", "lib")
				if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
					_ = os.Setenv("LD_LIBRARY_PATH", libDir+":"+existing)
				} else {
					_ = os.Setenv("LD_LIBRARY_PATH", libDir)
				}
			}

			// Validate VM assets when not in sandbox mode.
			if !sandboxMode {
				if rootfsPath == "" || kernelPath == "" {
					return fmt.Errorf(
						"daemon start: libkrun requires --rootfs and --kernel.\n" +
							"  Run `nexus daemon start` (auto-provisions assets) or supply the flags.",
					)
				}
				if _, err := os.Stat(rootfsPath); err != nil {
					return fmt.Errorf("daemon start: rootfs %q not found: %w\n  Run `nexus daemon start` first or supply --rootfs", rootfsPath, err)
				}
				if _, err := os.Stat(kernelPath); err != nil {
					return fmt.Errorf("daemon start: kernel %q not found: %w\n  Run `nexus daemon start` first or supply --kernel", kernelPath, err)
				}
			}

			effectiveDriver := driver
			if effectiveDriver == "" && sandboxMode {
				effectiveDriver = "sandbox"
			}

			cfg := daemon.Config{
				DBPath:          dbPath,
				SocketPath:      socketPath,
				KernelPath:      kernelPath,
				RootFSPath:      rootfsPath,
				WorkDirRoot:     workDirRoot,
				BasesDir:        basesDir,
				NodeName:        nodeName,
				Network:         netCfg,
				Driver:          effectiveDriver,
				EmbeddedAgentFn: EmbeddedAgentFn,
			}

			// Inject/refresh the guest agent binary into rootfs.ext4.
			if !sandboxMode && !isForegroundChild {
				if err := ensureGuestAgent(cfg.RootFSPath); err != nil {
					return fmt.Errorf("daemon start: guest agent refresh: %w", err)
				}

				// Pre-bake developer tools into the base rootfs so workspaces start
				// instantly instead of spending 5-10 min on apt-get/npm install.
				// CI/provisioning lanes always skip bake to avoid long synchronous daemon
				// startup. Local/dev environments can still force bake explicitly.
				skipLibkrunBake := strings.EqualFold(strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_SKIP_BAKE")), "1") ||
					strings.EqualFold(strings.TrimSpace(os.Getenv("NEXUS_LIBKRUN_SKIP_BAKE")), "true") ||
					strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "1") ||
					strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "true")
				if isLibkrun && LibkrunBakeFn != nil && !skipLibkrunBake {
					LibkrunBakeFn(cfg.RootFSPath, cfg.KernelPath, func(status, message string) {
						emitPhase("rootfs-bake", status, message)
					})
				} else if isLibkrun && skipLibkrunBake {
					reason := "NEXUS_LIBKRUN_SKIP_BAKE set"
					if strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "1") ||
						strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "true") {
						reason = "CI environment"
					}
					emitPhase("rootfs-bake", "ok", "skipped ("+reason+")")
				}

				// rootfs-dir seeding is only required for legacy virtiofs mode.
				// Hybrid mode (block rootfs + virtiofs workspace share) does not use
				// rootfs-dir; skip the slow debugfs rdump step.
				if !isLibkrun {
					if err := ensureRootfsDirSeeded(cfg.RootFSPath, DefaultVMRootfsDirPath); err != nil {
						return fmt.Errorf("daemon start: rootfs-dir seed: %w", err)
					}
					if err := ensureGuestAgentRootfsDir(DefaultVMRootfsDirPath); err != nil {
						return fmt.Errorf("daemon start: rootfs-dir guest agent refresh: %w", err)
					}
				}
			}

			// Self-daemonize: re-exec in background, wait for socket, return.
			if !foreground && !isForegroundChild {
				return launchDaemonBackground(cmd.OutOrStdout(), socketPath, jsonOutput, readyTimeout)
			}

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
		},
	}

	cmd.Flags().StringVar(&dbPath, "db", filepath.Join(defaultData, "nexus.db"), "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", filepath.Join(defaultData, "nexusd.sock"), "Unix socket path")
	cmd.Flags().StringVar(&kernelPath, "kernel", os.Getenv("NEXUS_VM_KERNEL"), "VM kernel image path")
	cmd.Flags().StringVar(&rootfsPath, "rootfs", os.Getenv("NEXUS_VM_ROOTFS"), "VM rootfs image path")
	cmd.Flags().StringVar(&workDirRoot, "workdir-root", "", "VM work dir root (default: $state/libkrun-vms)")
	cmd.Flags().StringVar(&nodeName, "node-name", hostName(), "Node name for identity")
	cmd.Flags().BoolVar(&network, "network", true, "Enable TCP/WebSocket network listener")
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "Network listener bind address")
	cmd.Flags().IntVar(&port, "port", 7777, "Network listener port")
	cmd.Flags().StringVar(&tlsMode, "tls", "off", "TLS mode: off | auto | required")
	cmd.Flags().StringVar(&token, "token", "", "Static bearer token (default: auto-generated and stored)")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "TLS certificate file (PEM) for required mode")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "TLS key file (PEM) for required mode")
	cmd.Flags().BoolVar(&sandboxMode, "sandbox", false, "Use process sandbox backend (internal/testing)")
	_ = cmd.Flags().MarkHidden("sandbox")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Stay in foreground instead of self-daemonizing")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit structured JSON phase events during bootstrap (for RemoteProvisioner / CI)")
	cmd.Flags().StringVar(&driver, "driver", "", "Runtime driver override: libkrun | sandbox (default: auto)")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", 30*time.Second, "Max time to wait for daemon socket readiness in self-daemonizing mode")

	return cmd
}

func preferredLibkrunStorageRoot() (string, bool) {
	const root = "/data/nexus"
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return root, true
}

func promoteVMAssetToDir(srcPath, destDir string) (string, error) {
	src := strings.TrimSpace(srcPath)
	if src == "" {
		return "", fmt.Errorf("source path is empty")
	}
	if strings.TrimSpace(destDir) == "" {
		return src, nil
	}
	base := filepath.Base(src)
	dest := filepath.Join(destDir, base)
	if filepath.Clean(src) == filepath.Clean(dest) {
		return dest, nil
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("stat source %s: %w", src, err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	if dstInfo, err := os.Stat(dest); err == nil {
		// Reuse existing promoted asset if it matches source freshness and size.
		if dstInfo.Size() == srcInfo.Size() && !srcInfo.ModTime().After(dstInfo.ModTime()) {
			return dest, nil
		}
	}

	tmp := dest + ".tmp"
	_ = os.Remove(tmp)
	out, cpErr := exec.Command("cp", "--reflink=auto", "--sparse=auto", src, tmp).CombinedOutput()
	if cpErr != nil {
		return "", fmt.Errorf("cp %s -> %s: %w: %s", src, tmp, cpErr, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("rename %s -> %s: %w", tmp, dest, err)
	}
	return dest, nil
}

// launchDaemonBackground re-execs the current binary with NEXUS_DAEMON_FOREGROUND=1,
// detached from the terminal (new session, stdout/stderr → log file).
func launchDaemonBackground(out io.Writer, socketPath string, jsonOut bool, readyTimeout time.Duration) error {
	emitPhase := func(status, message string) {
		if jsonOut {
			fmt.Fprintf(out, `{"phase":"daemon-launch","status":%q,"message":%q}`+"\n", status, message)
		} else {
			fmt.Fprintf(out, "daemon-launch: %s: %s\n", status, message)
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemon start: resolve executable: %w", err)
	}

	logDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("daemon start: create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("daemon start: open log: %w", err)
	}
	defer logFile.Close()

	child := exec.Command(exe, os.Args[1:]...)
	child.Env = append(os.Environ(), daemonForegroundEnv+"=1")
	child.Stdout = logFile
	child.Stderr = logFile
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := child.Start(); err != nil {
		return fmt.Errorf("daemon start: launch background process: %w", err)
	}

	emitPhase("start", fmt.Sprintf("background process pid=%d", child.Process.Pid))

	// Wait in background so ProcessState is populated if the child exits early.
	go func() {
		_ = child.Wait()
	}()

	// Poll for socket readiness.
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if _, statErr := os.Stat(socketPath); statErr == nil {
			emitPhase("ok", fmt.Sprintf("pid=%d log=%s", child.Process.Pid, logPath))
			return nil
		}
		if child.ProcessState != nil {
			return fmt.Errorf("daemon exited unexpectedly (exit %d) — check log: %s",
				child.ProcessState.ExitCode(), logPath)
		}
	}
	return fmt.Errorf("daemon did not become ready within %s — check log: %s", readyTimeout, logPath)
}

// agentHashFile returns a user-writable path for the agent SHA-256 cache.
func agentHashFile() string {
	return filepath.Join(defaultDataDir(), "rootfs-agent.sha256")
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

func hostName() string {
	name, _ := os.Hostname()
	return name
}

// ensureGuestAgent writes the guest-agent binary into the rootfs image at
// /usr/local/bin/nexus-guest-agent. Injection is skipped when the
// binary hash matches the stored hash (avoids slow debugfs round-trips).
func ensureGuestAgent(rootfsPath string) error {
	rootfsPath = strings.TrimSpace(rootfsPath)
	if rootfsPath == "" {
		return fmt.Errorf("rootfs path is required")
	}
	if _, err := os.Stat(rootfsPath); err != nil {
		return fmt.Errorf("rootfs not found at %q: %w", rootfsPath, err)
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return fmt.Errorf("debugfs is required to refresh guest agent payload: %w", err)
	}

	agentPath, err := resolveGuestAgentBinary()
	if err != nil {
		return err
	}

	hashFile := agentHashFile()
	agentHash, hashErr := sha256HexFile(agentPath)
	if hashErr == nil {
		if stored, readErr := os.ReadFile(hashFile); readErr == nil &&
			strings.TrimSpace(string(stored)) == agentHash {
			return nil
		}
	}

	// Repair any dirty journal before writing with debugfs.
	_ = exec.Command("e2fsck", "-f", "-y", rootfsPath).Run()

	for _, cmd := range []string{
		"mkdir /usr/local",
		"mkdir /usr/local/bin",
		"mkdir /workspace",
		"rm /usr/local/bin/nexus-guest-agent",
	} {
		_ = exec.Command("debugfs", "-w", "-R", cmd, rootfsPath).Run()
	}

	if out, err := exec.Command("debugfs", "-w", "-R", fmt.Sprintf("write %s /usr/local/bin/nexus-guest-agent", agentPath), rootfsPath).CombinedOutput(); err != nil {
		return fmt.Errorf("write guest agent into rootfs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("debugfs", "-w", "-R", "sif /usr/local/bin/nexus-guest-agent mode 0100755", rootfsPath).CombinedOutput(); err != nil {
		return fmt.Errorf("set mode on guest agent in rootfs: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if hashErr == nil {
		if dir := filepath.Dir(hashFile); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		_ = os.WriteFile(hashFile, []byte(agentHash+"\n"), 0o644)
	}
	return nil
}

// ensureGuestAgentRootfsDir writes the guest-agent binary into the rootfs
// directory at /usr/local/bin/nexus-guest-agent for set_root+set_exec mode.
func ensureGuestAgentRootfsDir(rootfsDir string) error {
	rootfsDir = strings.TrimSpace(rootfsDir)
	if rootfsDir == "" {
		return nil
	}
	fi, err := os.Stat(rootfsDir)
	if err != nil {
		// Hybrid mode doesn't need rootfs-dir; skip gracefully.
		return nil
	}
	if !fi.IsDir() {
		return fmt.Errorf("rootfs-dir %q is not a directory", rootfsDir)
	}

	agentPath, err := resolveGuestAgentBinary()
	if err != nil {
		return err
	}
	src, err := os.ReadFile(agentPath)
	if err != nil {
		return fmt.Errorf("read guest agent source: %w", err)
	}

	dst := filepath.Join(rootfsDir, "usr", "local", "bin", "nexus-guest-agent")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir guest agent dir in rootfs-dir: %w", err)
	}

	needsWrite := true
	if existing, err := os.ReadFile(dst); err == nil {
		if sha256.Sum256(existing) == sha256.Sum256(src) {
			needsWrite = false
		}
	}

	if needsWrite {
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, src, 0o755); err != nil {
			return fmt.Errorf("write guest agent into rootfs-dir: %w", err)
		}
		if err := os.Rename(tmp, dst); err != nil {
			return fmt.Errorf("install guest agent into rootfs-dir: %w", err)
		}
	}
	_ = os.MkdirAll(filepath.Join(rootfsDir, "var", "lib"), 0o755)
	// Virtiofs-root launches cannot reliably run apt/dpkg ownership changes on
	// rootfs paths. Only write toolchain stamps when required binaries already
	// exist in rootfs-dir (from a successful bake/seed), so we never mask
	// genuinely missing toolchains.
	required := requiredRootfsDirToolchainPaths(rootfsDir)
	allPresent := true
	for _, p := range required {
		if _, err := os.Stat(p); err != nil {
			allPresent = false
			break
		}
	}
	baseStamp := filepath.Join(rootfsDir, "var", "lib", "nexus-tools-base-v7")
	legacyBaseStampV6 := filepath.Join(rootfsDir, "var", "lib", "nexus-tools-base-v6")
	legacyBaseStampV5 := filepath.Join(rootfsDir, "var", "lib", "nexus-tools-base-v5")
	legacyOptionalStamp := filepath.Join(rootfsDir, "var", "lib", "nexus-tools-optional-v5")
	if allPresent {
		_ = os.WriteFile(baseStamp, []byte("ok\n"), 0o644)
		_ = os.Remove(legacyBaseStampV6)
		_ = os.Remove(legacyBaseStampV5)
		_ = os.Remove(legacyOptionalStamp)
	} else {
		_ = os.Remove(baseStamp)
		_ = os.Remove(legacyBaseStampV6)
		_ = os.Remove(legacyBaseStampV5)
		_ = os.Remove(legacyOptionalStamp)
	}
	return nil
}

func ensureRootfsDirSeeded(rootfsExt4, rootfsDir string) error {
	rootfsExt4 = strings.TrimSpace(rootfsExt4)
	rootfsDir = strings.TrimSpace(rootfsDir)
	if rootfsExt4 == "" || rootfsDir == "" {
		return nil
	}
	if _, err := os.Stat(rootfsExt4); err != nil {
		return nil
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return nil
	}

	required := requiredRootfsDirToolchainPaths(rootfsDir)
	allPresent := true
	for _, p := range required {
		if _, err := os.Stat(p); err != nil {
			allPresent = false
			break
		}
	}
	if allPresent {
		return nil
	}

	parent := filepath.Dir(rootfsDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create rootfs-dir parent: %w", err)
	}

	// Acquire a per-user flock so parallel daemon starts (e2e tests) don't race.
	lockPath := filepath.Join(parent, "rootfs-dir.seed.lock")
	unlock, err := acquireFileLock(lockPath)
	if err != nil {
		return fmt.Errorf("acquire rootfs-dir seed lock: %w", err)
	}
	defer unlock()

	// Re-check after acquiring the lock: another daemon may have finished.
	allPresent = true
	for _, p := range required {
		if _, err := os.Stat(p); err != nil {
			allPresent = false
			break
		}
	}
	if allPresent {
		return nil
	}

	tmpDir, err := os.MkdirTemp(parent, "rootfs-dir-seed-*")
	if err != nil {
		return fmt.Errorf("create rootfs-dir temp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("debugfs", "-R", fmt.Sprintf("rdump / %s", tmpDir), rootfsExt4)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("seed rootfs-dir from ext4: %w: %s", err, strings.TrimSpace(string(out)))
	}

	backupDir := rootfsDir + ".bak"
	_ = os.RemoveAll(backupDir)
	if _, err := os.Stat(rootfsDir); err == nil {
		if err := os.Rename(rootfsDir, backupDir); err != nil {
			return fmt.Errorf("backup existing rootfs-dir: %w", err)
		}
	}
	if err := os.Rename(tmpDir, rootfsDir); err != nil {
		if _, statErr := os.Stat(backupDir); statErr == nil {
			_ = os.Rename(backupDir, rootfsDir)
		}
		return fmt.Errorf("activate seeded rootfs-dir: %w", err)
	}
	_ = os.RemoveAll(backupDir)
	return nil
}

// acquireFileLock acquires an exclusive flock on lockPath and returns an
// unlock function. The lock is released when unlock is called.
func acquireFileLock(lockPath string) (func(), error) {
	f, err := os.Create(lockPath)
	if err != nil {
		return nil, fmt.Errorf("create lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func requiredRootfsDirToolchainPaths(rootfsDir string) []string {
	return []string{
		filepath.Join(rootfsDir, "usr", "bin", "docker"),
		filepath.Join(rootfsDir, "usr", "bin", "dockerd"),
		filepath.Join(rootfsDir, "usr", "bin", "node"),
		filepath.Join(rootfsDir, "usr", "bin", "npm"),
		filepath.Join(rootfsDir, "usr", "local", "bin", "opencode"),
		filepath.Join(rootfsDir, "usr", "local", "bin", "codex"),
		filepath.Join(rootfsDir, "usr", "local", "bin", "claude"),
	}
}

// sha256HexFile returns the hex-encoded SHA-256 digest of the file at path.
func sha256HexFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func resolveGuestAgentBinary() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("NEXUS_GUEST_AGENT_BIN")); raw != "" {
		if _, err := os.Stat(raw); err != nil {
			return "", fmt.Errorf("NEXUS_GUEST_AGENT_BIN=%s: %w", raw, err)
		}
		return raw, nil
	}
	if EmbeddedAgentFn == nil {
		return "", fmt.Errorf("guest agent binary is not embedded in this build (linux/amd64 or linux/arm64 required)")
	}
	data := EmbeddedAgentFn()
	if len(data) == 0 {
		return "", fmt.Errorf("embedded guest agent is empty (build error)")
	}

	home, _ := os.UserHomeDir()
	dest := filepath.Join(home, ".local", "bin", "nexus-guest-agent")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}

	newHash := sha256.Sum256(data)
	needsWrite := true
	if existing, err := os.ReadFile(dest); err == nil {
		existingHash := sha256.Sum256(existing)
		needsWrite = existingHash != newHash
	}
	if needsWrite {
		tmp := dest + ".tmp"
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return "", fmt.Errorf("write embedded agent: %w", err)
		}
		if err := os.Rename(tmp, dest); err != nil {
			return "", fmt.Errorf("install embedded agent: %w", err)
		}
	}
	return dest, nil
}
