//go:build darwin

package macvm

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
	vmnet "github.com/oursky/nexus/packages/nexus/internal/vm/net"
)

type spawnConfig struct {
	workspaceID   string
	workDir       string
	sockDir       string // short temp dir for all Unix sockets (macOS 104-char path limit)
	rootFSPath    string
	workspacePath string
	configDir     string
	libDir        string
}

// spawnVM boots a libkrun microVM for the given workspace (virtio-fs direct layout)
// by starting a dedicated subprocess (_macvm-runner). Running the VM in a subprocess
// means each VM is fully isolated: stopping a VM is simply killing the subprocess,
// with no goroutine leaks and no libkrun global-state conflicts between sequential VMs.
func spawnVM(ctx context.Context, cfg spawnConfig) (*vmInstance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	libkrunPath, libkrunfwPath := libkrun.LibPaths(cfg.libDir)
	if _, err := os.Stat(libkrunPath); err != nil {
		return nil, fmt.Errorf(
			"libkrun.dylib not found in %s; run daemon start once (embedded extract) or task stage:libkrun-macos (%w)",
			cfg.libDir, err,
		)
	}
	if _, err := os.Stat(libkrunfwPath); err != nil {
		return nil, fmt.Errorf(
			"libkrunfw.dylib not found in %s; run daemon start once (embedded extract) or task stage:libkrun-macos (%w)",
			cfg.libDir, err,
		)
	}

	dockerExt := filepath.Join(cfg.workDir, "docker-data.ext4")
	skipDocker, err := ensureDockerDataExt4(dockerExt)
	if err != nil {
		return nil, fmt.Errorf("macvm: docker-data image: %w", err)
	}

	raiseSpawnFDLimits()
	if cfg.libDir != "" {
		prev := os.Getenv("DYLD_LIBRARY_PATH")
		if prev == "" {
			_ = os.Setenv("DYLD_LIBRARY_PATH", cfg.libDir)
		} else {
			_ = os.Setenv("DYLD_LIBRARY_PATH", cfg.libDir+":"+prev)
		}
	}

	sshHostPort, err := pickTCPPort127()
	if err != nil {
		return nil, fmt.Errorf("macvm: pick ssh host port: %w", err)
	}
	agentFwdPort, err := pickTCPPort127()
	if err != nil {
		return nil, fmt.Errorf("macvm: pick agent host port: %w", err)
	}

	gvproxyBin, err := vmnet.FindGVProxy(true)
	if err != nil {
		return nil, fmt.Errorf("macvm: gvproxy: %w", err)
	}
	sockGV := filepath.Join(cfg.sockDir, "gvproxy.sock")
	gvp, err := vmnet.StartGVProxy(gvproxyBin, sockGV)
	if err != nil {
		return nil, fmt.Errorf("macvm: start gvproxy: %w", err)
	}

	if err := gvp.ExposeTCPForward(sshHostPort, guestSSHPortGuest); err != nil {
		_ = gvp.Stop()
		return nil, fmt.Errorf("macvm: gvproxy ssh=%d→guest:%d: %w", sshHostPort, guestSSHPortGuest, err)
	}
	if err := gvp.ExposeTCPForward(agentFwdPort, guestAgentTCPPort); err != nil {
		_ = gvp.Stop()
		return nil, fmt.Errorf("macvm: gvproxy agent tcp=%d→guest:%d: %w", agentFwdPort, guestAgentTCPPort, err)
	}

	// Resolve custom kernel (optional).
	var customKernelPath string
	var customKernelFormat uint32 = libkrun.KernelFormatRaw
	for _, kp := range customKernelCandidates(cfg.libDir, cfg.workDir) {
		if _, statErr := os.Stat(kp); statErr == nil {
			customKernelPath = kp
			if runtime.GOARCH == "amd64" {
				customKernelFormat = libkrun.KernelFormatElf
			}
			break
		}
	}

	// Build guest environment.
	guestEnv := []string{
		"NEXUS_CONTAINER_MODE=1",
		"NEXUS_WORKSPACE_MODE=virtiofs",
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		// The agent runs as a child of /init.krun (not as PID 1), so the
		// default resolveListener() path only tries vsock once and falls back to
		// TCP. Force vsock with retries so the agent reliably listens on the
		// expected vsock port even when the vsock device needs a moment to init.
		"AGENT_REQUIRE_VSOCK=1",
		// TCP fallback port must match guestAgentTCPPort so gvproxy can forward.
		fmt.Sprintf("AGENT_PORT=%d", guestAgentTCPPort),
	}
	if skipDocker {
		guestEnv = append(guestEnv, "NEXUS_VIRTIOFS_SKIP_DOCKER=1")
	} else {
		guestEnv = append(guestEnv, "NEXUS_DOCKER_DEV=/dev/vdb")
	}
	if cfg.configDir != "" {
		guestEnv = append(guestEnv, "NEXUS_CONFIG_TAG=nexus-host-config")
	}
	// macOS VMs use a pre-baked rootfs; skip the heavy apt-get package installation.
	guestEnv = append(guestEnv, "NEXUS_SKIP_BASE_PACKAGES=1")

	// Determine log level for subprocess.
	var logLevel uint32
	if os.Getenv("NEXUS_LIBKRUN_LOG") != "" {
		logLevel = 4
	}

	dockerDataPath := ""
	if !skipDocker {
		dockerDataPath = dockerExt
	}

	runnerCfg := macVMRunnerConfig{
		LibkrunPath:        libkrunPath,
		LibkrunfwPath:      libkrunfwPath,
		WorkspaceID:        cfg.workspaceID,
		RootFSPath:         cfg.rootFSPath,
		DockerDataPath:     dockerDataPath,
		WorkspacePath:      cfg.workspacePath,
		ConfigDir:          cfg.configDir,
		SockDir:            cfg.sockDir,
		GVProxySockPath:    sockGV,
		MemMiB:             memMiBForEnv(),
		VCPUs:              vcpusForEnv(),
		CustomKernelPath:   customKernelPath,
		CustomKernelFormat: customKernelFormat,
		AgentPath:          "/usr/local/bin/nexus-guest-agent",
		GuestEnv:           guestEnv,
		LogLevel:           logLevel,
	}

	configPath := filepath.Join(cfg.sockDir, "vmrunner.json")
	if err := writeMacVMRunnerConfig(configPath, runnerCfg); err != nil {
		_ = gvp.Stop()
		return nil, fmt.Errorf("macvm: write runner config: %w", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		_ = gvp.Stop()
		return nil, fmt.Errorf("macvm: find executable: %w", err)
	}

	// Redirect subprocess output to a log file, not to the daemon's pipe.
	// Using os.Stderr would keep the test framework's I/O pipe open until the
	// subprocess (and any libkrun internal threads) fully exit, causing a
	// "Test I/O incomplete" failure. A regular file does not block.
	serialLogPath := filepath.Join(cfg.workDir, "hvc0.log")
	logFile, logErr := os.Create(serialLogPath)

	subCmd := exec.CommandContext(context.Background(), execPath, "_macvm-runner", configPath) //nolint:forbidigo
	if logErr == nil {
		subCmd.Stdout = logFile
		subCmd.Stderr = logFile
	} else {
		log.Printf("macvm: create serial log %s: %v (falling back to stderr)", serialLogPath, logErr)
		subCmd.Stdout = os.Stderr
		subCmd.Stderr = os.Stderr
	}
	// Pass libDir in DYLD_LIBRARY_PATH so the subprocess can load the dylibs.
	subEnv := os.Environ()
	subCmd.Env = subEnv

	if err := subCmd.Start(); err != nil {
		_ = gvp.Stop()
		return nil, fmt.Errorf("macvm: start vm subprocess: %w", err)
	}

	subPid := subCmd.Process.Pid
	log.Printf("macvm: started workspaceID=%s pid=%d sshPort=%d agentPort=%d",
		cfg.workspaceID, subPid, sshHostPort, agentFwdPort)

	done := make(chan struct{})
	go func() {
		defer func() {
			_ = gvp.Stop()
			if logFile != nil {
				_ = logFile.Close()
			}
			close(done)
		}()
		if err := subCmd.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("macvm: vm subprocess exited workspace=%s: %v", cfg.workspaceID, err)
		}
	}()

	inst := &vmInstance{
		workspaceID: cfg.workspaceID,
		workDir:     cfg.workDir,
		configDir:   cfg.configDir,
		sockDir:     cfg.sockDir,

		guestSSHPort: sshHostPort,
		agentPort:    agentFwdPort,
		pid:          subPid,
		stop: func() {
			if subCmd.Process != nil {
				_ = subCmd.Process.Kill()
			}
			_ = gvp.Stop()
			// Wait for the subprocess goroutine to finish after kill.
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				log.Printf("macvm: stop: subprocess did not exit workspace=%s", cfg.workspaceID)
			}
		},
		done: done,
	}

	if err := waitAgentListening(ctx, cfg.sockDir, agentFwdPort); err != nil {
		inst.stop()
		<-done
		return nil, fmt.Errorf("macvm: agent not reachable: %w", err)
	}
	return inst, nil
}

func pickTCPPort127() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

func customKernelCandidates(libDir, wsWorkDir string) []string {
	shareParent := filepath.Dir(libDir)
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(wsWorkDir, "nexus-vm-kernel"),
		filepath.Join(shareParent, "nexus-vm-kernel"),
		filepath.Join(home, ".cache", "nexus", "kernels", "Image-custom"),
	}
}

// memMiBForEnv returns the VM memory in MiB, honouring the test override env var.
func memMiBForEnv() uint32 {
	if v := os.Getenv("NEXUS_LIBKRUN_MEM_MIB"); v != "" {
		var n uint32
		_, _ = fmt.Sscan(v, &n)
		if n > 0 {
			return n
		}
	}
	return 4096
}

// vcpusForEnv returns the vCPU count, honouring the test override env var.
func vcpusForEnv() uint8 {
	if v := os.Getenv("NEXUS_LIBKRUN_VCPUS"); v != "" {
		var n uint8
		_, _ = fmt.Sscan(v, &n)
		if n > 0 {
			return n
		}
	}
	return 2
}
