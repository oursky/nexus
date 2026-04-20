package daemon

import (
	"context"
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

	"github.com/inizio/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/inizio/nexus/packages/nexus/internal/daemon"
	"github.com/spf13/cobra"
)

// daemonForegroundEnv is set to "1" in the re-exec'd child process to signal
// it should skip setup and run the daemon in the foreground.
const daemonForegroundEnv = "NEXUS_DAEMON_FOREGROUND"

// StartSetupFn, if non-nil, is called once before the daemon starts to
// provision host prerequisites (Firecracker binary, kernel, rootfs, networking).
// On Linux this is wired to runSetupFirecracker by the main package.
var StartSetupFn func(w io.Writer) error

// EmbeddedAgentFn, if non-nil, returns the embedded nexus-firecracker-agent
// binary bytes.  On Linux builds this is always set by daemon_hooks_linux.go.
var EmbeddedAgentFn func() []byte

// defaultVMKernelPath is the canonical kernel location after daemon setup.
const defaultVMKernelPath = "/var/lib/nexus/vmlinux.bin"

// defaultVMRootfsPath is the canonical rootfs location after daemon setup.
const defaultVMRootfsPath = "/var/lib/nexus/rootfs.ext4"

func startCommand() *cobra.Command {
	var (
		dbPath      string
		socketPath  string
		fcBin       string
		kernelPath  string
		rootfsPath  string
		workDirRoot string
		nodeName    string
		network     bool
		bind        string
		port        int
		tlsMode     string
		token       string
		tlsCert     string
		tlsKey      string
		foreground  bool // --foreground: stay blocking instead of self-daemonizing
		sandboxMode bool // internal: use process sandbox backend instead of Firecracker
	)

	defaultData := defaultDataDir()

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the Nexus daemon (auto-provisions prerequisites; listens on 127.0.0.1:7777 by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			isForegroundChild := os.Getenv(daemonForegroundEnv) == "1"

			// Auto-provision host prerequisites (Firecracker binary, kernel,
			// rootfs, networking).  Skipped in the background child process
			// since the parent already ran it interactively.
			if !isForegroundChild && StartSetupFn != nil {
				if err := StartSetupFn(cmd.ErrOrStderr()); err != nil {
					return fmt.Errorf("daemon start: host setup: %w", err)
				}
			}

			// Apply canonical default paths only in production Linux builds
			// where StartSetupFn has already provisioned the assets.
			// Test binaries (no StartSetupFn) skip this so the daemon starts
			// in sandbox mode when no explicit paths are provided.
			if StartSetupFn != nil && runtime.GOOS != "darwin" {
				if kernelPath == "" {
					kernelPath = defaultVMKernelPath
				}
				if rootfsPath == "" {
					rootfsPath = defaultVMRootfsPath
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

			// ── Firecracker vs sandbox decision ────────────────────────────────
			// Sandbox is an internal backend (--sandbox flag). Without it,
			// Firecracker is the only supported runtime.
			//
			// macOS: Firecracker is not yet supported. Without --sandbox the
			// daemon refuses to start so that tests skip cleanly instead of
			// silently running against a different backend.
			if !sandboxMode {
				if runtime.GOOS == "darwin" {
					return fmt.Errorf(
						"daemon start: Firecracker is not supported on macOS (darwin).\n" +
							"  Use --sandbox for local development/testing (internal flag).",
					)
				}
				if rootfsPath == "" || kernelPath == "" {
					return fmt.Errorf(
						"daemon start: Firecracker requires --rootfs and --kernel (or NEXUS_FIRECRACKER_ROOTFS / NEXUS_FIRECRACKER_KERNEL).\n" +
							"  Run `nexus setup` first, or pass --sandbox to use the process sandbox backend.",
					)
				}
				// Require the image files to be present — no silent fallback.
				if _, err := os.Stat(rootfsPath); err != nil {
					return fmt.Errorf("daemon start: rootfs %q not found: %w\n  Run `nexus setup` first or supply --rootfs", rootfsPath, err)
				}
				if _, err := os.Stat(kernelPath); err != nil {
					return fmt.Errorf("daemon start: kernel %q not found: %w\n  Run `nexus setup` first or supply --kernel", kernelPath, err)
				}
			}

			firecrackerEnabled := !sandboxMode

			// Pin firecracker to the exact binary our setup installed so PATH
			// order (e.g. nix) cannot shadow it with a different version.
			if fcBin == "firecracker" && StartSetupFn != nil {
				home, _ := os.UserHomeDir()
				installed := filepath.Join(home, ".local", "bin", "firecracker")
				if _, err := os.Stat(installed); err == nil {
					fcBin = installed
				}
			}

			cfg := daemon.Config{
				DBPath:             dbPath,
				SocketPath:         socketPath,
				FirecrackerEnabled: firecrackerEnabled,
				FirecrackerBin:     fcBin,
				KernelPath:         kernelPath,
				RootFSPath:         rootfsPath,
				WorkDirRoot:        workDirRoot,
				NodeName:           nodeName,
				Network:            netCfg,
			}

			if firecrackerEnabled {
				if err := ensureFirecrackerGuestAgent(cfg.RootFSPath); err != nil {
					return fmt.Errorf("daemon start: guest agent refresh: %w", err)
				}
			}

			// Self-daemonize: re-exec in background, wait for socket, return.
			// The parent runs this interactively (setup, sudo, etc.) then
			// exits once the daemon is ready.  Use --foreground to opt out.
			if !foreground && !isForegroundChild {
				return launchDaemonBackground(cmd.OutOrStdout(), socketPath)
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
	cmd.Flags().StringVar(&fcBin, "firecracker-bin", "firecracker", "Firecracker binary name")
	// Env-var overrides; final defaults applied in RunE after setup runs.
	cmd.Flags().StringVar(&kernelPath, "kernel", os.Getenv("NEXUS_FIRECRACKER_KERNEL"), "Firecracker kernel image path (default: /var/lib/nexus/vmlinux.bin)")
	cmd.Flags().StringVar(&rootfsPath, "rootfs", os.Getenv("NEXUS_FIRECRACKER_ROOTFS"), "Firecracker rootfs image path (default: /var/lib/nexus/rootfs.ext4)")
	cmd.Flags().StringVar(&workDirRoot, "workdir-root", filepath.Join(defaultData, "firecracker-vms"), "Firecracker VM work dir root")
	cmd.Flags().StringVar(&nodeName, "node-name", hostName(), "Node name for identity")
	cmd.Flags().BoolVar(&network, "network", true, "Enable TCP/WebSocket network listener")
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "Network listener bind address")
	cmd.Flags().IntVar(&port, "port", 7777, "Network listener port")
	cmd.Flags().StringVar(&tlsMode, "tls", "off", "TLS mode: off | auto | required")
	cmd.Flags().StringVar(&token, "token", "", "Static bearer token (default: auto-generated and stored)")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "TLS certificate file (PEM) for required mode")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "TLS key file (PEM) for required mode")
	cmd.Flags().BoolVar(&sandboxMode, "sandbox", false, "Use process sandbox backend instead of Firecracker (internal/testing)")
	_ = cmd.Flags().MarkHidden("sandbox")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Stay in foreground instead of self-daemonizing")

	return cmd
}

// launchDaemonBackground re-execs the current binary with NEXUS_DAEMON_FOREGROUND=1,
// detached from the terminal (new session, stdout/stderr → log file).
// It blocks until the daemon socket appears (ready) or a timeout is reached.
func launchDaemonBackground(out io.Writer, socketPath string) error {
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

	fmt.Fprintf(out, "daemon starting (PID %d)...\n", child.Process.Pid)

	// Poll for socket readiness (daemon writes it once bound).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if _, statErr := os.Stat(socketPath); statErr == nil {
			fmt.Fprintf(out, "daemon ready  pid=%d  log=%s\n", child.Process.Pid, logPath)
			return nil
		}
		// Detect early exit (startup failure).
		if child.ProcessState != nil {
			return fmt.Errorf("daemon exited unexpectedly (exit %d) — check log: %s",
				child.ProcessState.ExitCode(), logPath)
		}
	}
	return fmt.Errorf("daemon did not become ready within 30s — check log: %s", logPath)
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

// ensureFirecrackerGuestAgent writes the nexus-firecracker-agent binary into
// the rootfs image at /usr/local/bin/nexus-firecracker-agent.
//
// The kernel boots the agent directly via the init= boot argument
// ("init=/usr/local/bin/nexus-firecracker-agent"), so we no longer need to
// patch /sbin/init or write a wrapper script — just the binary itself.
func ensureFirecrackerGuestAgent(rootfsPath string) error {
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

	// Best effort fsck to recover from unclean loopback state.
	_ = exec.Command("e2fsck", "-fy", rootfsPath).Run()

	for _, cmd := range []string{
		"mkdir /usr/local",
		"mkdir /usr/local/bin",
		"mkdir /workspace",
		"rm /usr/local/bin/nexus-firecracker-agent",
	} {
		_ = exec.Command("debugfs", "-w", "-R", cmd, rootfsPath).Run()
	}

	if out, err := exec.Command("debugfs", "-w", "-R", fmt.Sprintf("write %s /usr/local/bin/nexus-firecracker-agent", agentPath), rootfsPath).CombinedOutput(); err != nil {
		return fmt.Errorf("write guest agent into rootfs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// debugfs write does not preserve source file permissions; set mode with sif.
	// 0100755 = S_IFREG | 0755 (regular file, rwxr-xr-x).
	if out, err := exec.Command("debugfs", "-w", "-R", "sif /usr/local/bin/nexus-firecracker-agent mode 0100755", rootfsPath).CombinedOutput(); err != nil {
		return fmt.Errorf("set mode on guest agent in rootfs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveGuestAgentBinary() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("NEXUS_FIRECRACKER_AGENT_BIN")); raw != "" {
		if _, err := os.Stat(raw); err != nil {
			return "", fmt.Errorf("NEXUS_FIRECRACKER_AGENT_BIN=%s: %w", raw, err)
		}
		return raw, nil
	}
	if EmbeddedAgentFn == nil {
		return "", fmt.Errorf("nexus-firecracker-agent binary is not embedded in this build (linux/amd64 or linux/arm64 required)")
	}
	data := EmbeddedAgentFn()
	if len(data) == 0 {
		return "", fmt.Errorf("embedded nexus-firecracker-agent is empty (build error)")
	}
	tmp, err := os.CreateTemp("", "nexus-firecracker-agent-*")
	if err != nil {
		return "", fmt.Errorf("create temp for embedded agent: %w", err)
	}
	dest := tmp.Name()
	_ = tmp.Close()
	if err := os.WriteFile(dest, data, 0o755); err != nil {
		return "", fmt.Errorf("write embedded agent: %w", err)
	}
	return dest, nil
}
