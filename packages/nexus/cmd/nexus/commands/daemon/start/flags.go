package start

import (
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

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

func registerStartFlags(cmd *cobra.Command) {
	defaultData := DefaultDataDir()

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
	cmd.Flags().StringVar(&driver, "driver", "", "Runtime driver override: vm | process | libkrun | sandbox (default: auto)")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", 30*time.Second, "Max time to wait for daemon socket readiness in self-daemonizing mode")
}

func hostName() string {
	name, _ := os.Hostname()
	return name
}
