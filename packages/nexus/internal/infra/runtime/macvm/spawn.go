//go:build darwin

package macvm

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
	vmnet "github.com/oursky/nexus/packages/nexus/internal/vm/net"
)

// libkrunLogOnce ensures krun_set_log_level is called at most once per
// process. libkrun uses Rust's env_logger, which panics if its global
// Builder::init is invoked a second time.
var libkrunLogOnce sync.Once

type spawnConfig struct {
	workspaceID   string
	workDir       string
	sockDir       string // short temp dir for all Unix sockets (macOS 104-char path limit)
	rootFSPath    string
	workspacePath string
	configDir     string
	libDir        string
}

// spawnVM boots a libkrun microVM for the given workspace (virtio-fs direct layout).
// Requires libkrun.dylib / libkrunfw.dylib in cfg.libDir (installed by daemon start from
// embedded smolvm dylibs). Dev fallback: ~/.smolvm/lib or task stage:libkrun-macos → Swift Resources.
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

	lib, err := libkrun.Load(libkrunPath, libkrunfwPath)
	if err != nil {
		return nil, fmt.Errorf("macvm: load libkrun: %w", err)
	}

	vmCtx, err := lib.NewContext()
	if err != nil {
		lib.Close()
		return nil, fmt.Errorf("macvm: new context: %w", err)
	}

	fail := func(e error) (*vmInstance, error) {
		_ = vmCtx.Free()
		lib.Close()
		return nil, e
	}

	libkrunLogOnce.Do(func() {
		logLevel := uint32(0)
		if os.Getenv("NEXUS_LIBKRUN_LOG") != "" {
			logLevel = 4
		}
		_ = lib.SetLogLevel(logLevel)
	})

	const vcpus, memMiB = 2, 4096
	if err := vmCtx.SetVMConfig(vcpus, memMiB); err != nil {
		return fail(fmt.Errorf("macvm: set_vm_config: %w", err))
	}
	if err := vmCtx.AddDisk("rootfs", cfg.rootFSPath, 0, false); err != nil {
		return fail(fmt.Errorf("macvm: add rootfs: %w", err))
	}
	if !skipDocker {
		if err := vmCtx.AddDisk("docker_data", dockerExt, 0, false); err != nil {
			return fail(fmt.Errorf("macvm: add docker-data: %w", err))
		}
	}

	if cfg.configDir != "" {
		if err := vmCtx.AddVirtioFS("nexus-host-config", cfg.configDir); err != nil {
			return fail(fmt.Errorf("macvm: virtiofs host config: %w", err))
		}
	}

	if err := vmCtx.SetRootDiskRemount("/dev/vda", "ext4", "rw"); err != nil {
		return fail(fmt.Errorf("macvm: root disk remount: %w", err))
	}

	if fi, err := os.Stat(cfg.workspacePath); err != nil || !fi.IsDir() {
		if err != nil {
			return fail(fmt.Errorf("macvm: workspace dir: %w", err))
		}
		return fail(fmt.Errorf("macvm: workspace path %q is not a directory", cfg.workspacePath))
	}
	if err := vmCtx.AddVirtioFS("nexus-workspace", cfg.workspacePath); err != nil {
		return fail(fmt.Errorf("macvm: virtiofs workspace: %w", err))
	}

	for _, kp := range customKernelCandidates(cfg.libDir, cfg.workDir) {
		if _, statErr := os.Stat(kp); statErr != nil {
			continue
		}
		var kfmt uint32 = libkrun.KernelFormatRaw
		if runtime.GOARCH == "amd64" {
			kfmt = libkrun.KernelFormatElf
		}
		if err := vmCtx.SetKernel(kp, kfmt, "", ""); err == nil {
			log.Printf("macvm: custom kernel=%s", kp)
			break
		}
	}

	gvproxyBin, err := vmnet.FindGVProxy(true)
	if err != nil {
		return fail(fmt.Errorf("macvm: gvproxy: %w", err))
	}
	sockGV := filepath.Join(cfg.sockDir, "gvproxy.sock")
	gvp, err := vmnet.StartGVProxy(gvproxyBin, sockGV)
	if err != nil {
		return fail(fmt.Errorf("macvm: start gvproxy: %w", err))
	}

	if err := vmCtx.AddNetUnixgram(sockGV, nil, libkrun.CompatNetFeatures, libkrun.NetFlagVFKit); err != nil {
		_ = gvp.Stop()
		return fail(fmt.Errorf("macvm: virtio-net: %w", err))
	}
	if err := gvp.ExposeTCPForward(sshHostPort, guestSSHPortGuest); err != nil {
		_ = gvp.Stop()
		return fail(fmt.Errorf("macvm: gvproxy ssh=%d→guest:%d: %w", sshHostPort, guestSSHPortGuest, err))
	}
	if err := gvp.ExposeTCPForward(agentFwdPort, guestAgentTCPPort); err != nil {
		_ = gvp.Stop()
		return fail(fmt.Errorf("macvm: gvproxy agent tcp=%d→guest:%d: %w", agentFwdPort, guestAgentTCPPort, err))
	}

	if err := vmCtx.AddVsockPort(agentVSockPort, agentSockPath(cfg.sockDir), true); err != nil {
		_ = gvp.Stop()
		return fail(fmt.Errorf("macvm: vsock agent: %w", err))
	}
	if err := vmCtx.AddVsockPort(spotlightVSockPort, spotlightSockPath(cfg.sockDir), true); err != nil {
		_ = gvp.Stop()
		return fail(fmt.Errorf("macvm: vsock spotlight: %w", err))
	}

	agentPath := "/usr/local/bin/nexus-guest-agent"
	guestEnv := []string{
		"NEXUS_CONTAINER_MODE=1",
		"NEXUS_WORKSPACE_MODE=virtiofs",
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
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
	if err := vmCtx.SetWorkdir("/"); err != nil {
		_ = gvp.Stop()
		return fail(fmt.Errorf("macvm: set workdir: %w", err))
	}
	if err := vmCtx.SetExec(agentPath, nil, guestEnv); err != nil {
		_ = gvp.Stop()
		return fail(fmt.Errorf("macvm: set_exec: %w", err))
	}

	done := make(chan struct{})
	go func() {
		defer func() {
			_ = gvp.Stop()
			_ = vmCtx.Free()
			lib.Close()
			close(done)
		}()
		runErr := vmCtx.StartEnter()
		if runErr != nil && ctx.Err() == nil {
			log.Printf("macvm: vm exited workspace=%s: %v", cfg.workspaceID, runErr)
		}
	}()

	inst := &vmInstance{
		workspaceID:  cfg.workspaceID,
		workDir:      cfg.workDir,
		configDir:    cfg.configDir,
		sockDir:      cfg.sockDir,

		guestSSHPort: sshHostPort,
		agentPort:    agentFwdPort,
		pid:          os.Getpid(),
		stop: func() {
			_ = gvp.Stop()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
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
