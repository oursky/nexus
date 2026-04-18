package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/inizio/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/inizio/nexus/packages/nexus/internal/daemon"
	"github.com/spf13/cobra"
)

func startCommand() *cobra.Command {
	var (
		dbPath      string
		socketPath  string
		firecracker bool
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
	)

	defaultData := defaultDataDir()

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the Nexus daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			cfg := daemon.Config{
				DBPath:             dbPath,
				SocketPath:         socketPath,
				FirecrackerEnabled: firecracker,
				FirecrackerBin:     fcBin,
				KernelPath:         kernelPath,
				RootFSPath:         rootfsPath,
				WorkDirRoot:        workDirRoot,
				NodeName:           nodeName,
				Network:            netCfg,
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
	cmd.Flags().BoolVar(&firecracker, "firecracker", false, "Enable Firecracker VM backend")
	cmd.Flags().StringVar(&fcBin, "firecracker-bin", "firecracker", "Firecracker binary name")
	cmd.Flags().StringVar(&kernelPath, "kernel", os.Getenv("NEXUS_FIRECRACKER_KERNEL"), "Firecracker kernel image path")
	cmd.Flags().StringVar(&rootfsPath, "rootfs", os.Getenv("NEXUS_FIRECRACKER_ROOTFS"), "Firecracker rootfs image path")
	cmd.Flags().StringVar(&workDirRoot, "workdir-root", filepath.Join(defaultData, "firecracker-vms"), "Firecracker VM work dir root")
	cmd.Flags().StringVar(&nodeName, "node-name", hostName(), "Node name for identity")
	cmd.Flags().BoolVar(&network, "network", false, "Enable TCP/WebSocket network listener")
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "Network listener bind address")
	cmd.Flags().IntVar(&port, "port", 7777, "Network listener port")
	cmd.Flags().StringVar(&tlsMode, "tls", "off", "TLS mode: off | auto | required")
	cmd.Flags().StringVar(&token, "token", "", "Static bearer token (default: auto-generated and stored)")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "TLS certificate file (PEM) for required mode")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "TLS key file (PEM) for required mode")

	return cmd
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
