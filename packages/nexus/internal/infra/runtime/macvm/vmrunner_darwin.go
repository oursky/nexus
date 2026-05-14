//go:build darwin

package macvm

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
)

// macVMRunnerConfig is the configuration passed from the daemon to the
// _macvm-runner subprocess via a JSON file. It contains everything needed
// to boot a libkrun microVM without any in-process state from the parent.
type macVMRunnerConfig struct {
	LibkrunPath   string `json:"libkrun_path"`
	LibkrunfwPath string `json:"libkrunfw_path"`

	WorkspaceID string `json:"workspace_id"`

	// Block devices
	RootFSPath     string `json:"rootfs_path"`
	DockerDataPath string `json:"docker_data_path,omitempty"` // empty = skip docker

	// VirtioFS shares
	WorkspacePath string `json:"workspace_path"`
	ConfigDir     string `json:"config_dir,omitempty"`

	// Sockets
	SockDir         string `json:"sock_dir"`
	GVProxySockPath string `json:"gvproxy_sock_path"`

	// VM sizing
	MemMiB uint32 `json:"mem_mib"`
	VCPUs  uint8  `json:"vcpus"`

	// Custom kernel (optional)
	CustomKernelPath   string `json:"custom_kernel_path,omitempty"`
	CustomKernelFormat uint32 `json:"custom_kernel_format,omitempty"`

	// Guest process
	AgentPath string   `json:"agent_path"`
	GuestEnv  []string `json:"guest_env"`

	// Logging
	LogLevel uint32 `json:"log_level,omitempty"`
}

func writeMacVMRunnerConfig(path string, cfg macVMRunnerConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// RunVMSubprocess is the entry point for the _macvm-runner internal subcommand.
// It reads the VM config from configPath, boots a libkrun microVM, and blocks
// in krun_start_enter until the VM exits (or until the process is killed).
//
// This function never returns normally — krun_start_enter either blocks until
// the VM exits naturally (returning a non-zero code for error) or calls exit()
// directly when the guest ACPI powers off. Either way the subprocess exits and
// the parent daemon's cmd.Wait() goroutine closes the done channel.
func RunVMSubprocess(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("[macvm-runner] read config %s: %v", configPath, err)
	}
	var cfg macVMRunnerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("[macvm-runner] parse config: %v", err)
	}

	lib, err := libkrun.Load(cfg.LibkrunPath, cfg.LibkrunfwPath)
	if err != nil {
		log.Fatalf("[macvm-runner] load libkrun: %v", err)
	}

	logLevel := cfg.LogLevel
	if logLevel == 0 && os.Getenv("NEXUS_LIBKRUN_LOG") != "" {
		logLevel = 4
	}
	_ = lib.SetLogLevel(logLevel)

	vmCtx, err := lib.NewContext()
	if err != nil {
		log.Fatalf("[macvm-runner] new context: %v", err)
	}

	vcpus := cfg.VCPUs
	if vcpus == 0 {
		vcpus = 2
	}
	memMiB := cfg.MemMiB
	if memMiB == 0 {
		memMiB = 4096
	}

	fatalf := func(format string, args ...any) {
		log.Fatalf("[macvm-runner] "+format, args...)
	}

	if err := vmCtx.SetVMConfig(vcpus, memMiB); err != nil {
		fatalf("set_vm_config: %v", err)
	}
	if err := vmCtx.AddDisk("rootfs", cfg.RootFSPath, 0, false); err != nil {
		fatalf("add rootfs: %v", err)
	}
	if cfg.DockerDataPath != "" {
		if err := vmCtx.AddDisk("docker_data", cfg.DockerDataPath, 0, false); err != nil {
			fatalf("add docker-data: %v", err)
		}
	}
	if cfg.ConfigDir != "" {
		if err := vmCtx.AddVirtioFS("nexus-host-config", cfg.ConfigDir); err != nil {
			fatalf("virtiofs host-config: %v", err)
		}
	}
	if err := vmCtx.SetRootDiskRemount("/dev/vda", "ext4", "rw"); err != nil {
		fatalf("root disk remount: %v", err)
	}
	if err := vmCtx.AddVirtioFS("nexus-workspace", cfg.WorkspacePath); err != nil {
		fatalf("virtiofs workspace: %v", err)
	}

	if cfg.CustomKernelPath != "" {
		kfmt := cfg.CustomKernelFormat
		if err := vmCtx.SetKernel(cfg.CustomKernelPath, kfmt, "", ""); err != nil {
			log.Printf("[macvm-runner] set custom kernel %s: %v (ignoring)", cfg.CustomKernelPath, err)
		} else {
			log.Printf("[macvm-runner] custom kernel=%s", cfg.CustomKernelPath)
		}
	}

	if err := vmCtx.AddNetUnixgram(cfg.GVProxySockPath, nil, libkrun.CompatNetFeatures, libkrun.NetFlagVFKit); err != nil {
		fatalf("virtio-net (gvproxy sock %s): %v", cfg.GVProxySockPath, err)
	}

	if err := vmCtx.AddVsockPort(agentVSockPort, agentSockPath(cfg.SockDir), true); err != nil {
		fatalf("vsock agent port %d: %v", agentVSockPort, err)
	}
	if err := vmCtx.AddVsockPort(spotlightVSockPort, spotlightSockPath(cfg.SockDir), true); err != nil {
		fatalf("vsock spotlight port %d: %v", spotlightVSockPort, err)
	}

	// Write VM serial console (HVC0) to a file so the test harness can read it.
	serialSock := filepath.Join(cfg.SockDir, "hvc0.sock")
	if err := vmCtx.SetConsoleOutput(serialSock); err != nil {
		log.Printf("[macvm-runner] set_console_output: %v (serial log unavailable)", err)
	}

	if err := vmCtx.SetWorkdir("/"); err != nil {
		fatalf("set workdir: %v", err)
	}
	agentPath := cfg.AgentPath
	if agentPath == "" {
		agentPath = "/usr/local/bin/nexus-guest-agent"
	}
	if err := vmCtx.SetExec(agentPath, nil, cfg.GuestEnv); err != nil {
		fatalf("set_exec: %v", err)
	}

	log.Printf("[macvm-runner] config: vcpus=%d mem_mib=%d rootfs=%s docker_data=%q workspace=%s config_dir=%q gvproxy_sock=%s",
		vcpus, memMiB, cfg.RootFSPath, cfg.DockerDataPath, cfg.WorkspacePath, cfg.ConfigDir, cfg.GVProxySockPath)

	// Pre-flight: verify disk images exist and are non-empty before entering
	// the VM. A 0-byte or missing rootfs causes krun_start_enter to return
	// EINVAL immediately, which is indistinguishable from a Hypervisor.framework
	// configuration error. Catching it here gives a clear diagnostic instead.
	for _, check := range []struct{ label, path string }{
		{"rootfs", cfg.RootFSPath},
		{"docker_data", cfg.DockerDataPath},
	} {
		if check.path == "" {
			continue
		}
		fi, err := os.Stat(check.path)
		if err != nil {
			log.Printf("[macvm-runner] pre-flight FAIL: %s not found: %v workspace=%s", check.label, err, cfg.WorkspaceID)
			os.Exit(2)
		}
		if fi.Size() == 0 {
			log.Printf("[macvm-runner] pre-flight FAIL: %s is 0 bytes at %s workspace=%s", check.label, check.path, cfg.WorkspaceID)
			os.Exit(2)
		}
		log.Printf("[macvm-runner] pre-flight ok: %s size=%d bytes", check.label, fi.Size())
	}

	// Log available disk space on the volume containing the workspace dirs
	// to help diagnose ENOSPC / EINVAL failures from krun_start_enter.
	if stat, err := diskUsage(filepath.Dir(cfg.RootFSPath)); err == nil {
		log.Printf("[macvm-runner] disk avail: %s avail=%d MiB total=%d MiB",
			filepath.Dir(cfg.RootFSPath), stat.avail>>20, stat.total>>20)
	}

	fmt.Fprintf(os.Stderr, "[macvm-runner] booting VM workspace=%s\n", cfg.WorkspaceID)

	// krun_start_enter blocks until the VM exits. If the guest triggers ACPI
	// poweroff, libkrun calls exit() directly and this line is never reached.
	// If it returns (error or clean exit), log and exit.
	if err := vmCtx.StartEnter(); err != nil {
		log.Printf("[macvm-runner] krun_start_enter exited workspace=%s: %v", cfg.WorkspaceID, err)
		os.Exit(1)
	}
	os.Exit(0)
}
