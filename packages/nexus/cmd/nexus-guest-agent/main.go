//go:build linux

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// vsock port constants shared with the host-side runtime drivers.
const (
	defaultAgentVSockPort     uint32 = 10789
	defaultSpotlightVSockPort uint32 = 10792
	vendingVSockPort          uint32 = 10790
	dockerCredHelperVSockPort uint32 = 10793
)

const defaultAgentPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func main() {
	// When invoked as docker-credential-nexus, act as the Docker credential
	// helper shim (proxies to the host via vsock).
	if strings.HasSuffix(os.Args[0], "docker-credential-nexus") {
		runDockerCredentialHelper()
		return
	}

	pid := os.Getpid()
	emitDiagnostic("agent boot pid=%d", pid)

	// Bind the main RPC listener early; the accept loop starts only after
	// bootstrap and SSH/git/docker sidecar listeners are ready so workspace.ready
	// cannot race ahead of /tmp/ssh-agent.sock (see startSSHAgentProxy).
	listener, transport, err := resolveListener()
	if err != nil {
		emitDiagnostic("agent listener setup failed: %v", err)
		log.Fatalf("Failed to listen: %v", err)
	}
	emitDiagnostic("agent listener bound transport=%s (bootstrap in progress)", transport)

	// Debug: log the kernel cmdline so we can verify nexus.baked=1 is present.
	if cmdline, err := os.ReadFile("/proc/cmdline"); err == nil {
		emitDiagnostic("agent kernel cmdline: %s", strings.TrimSpace(string(cmdline)))
	} else {
		emitDiagnostic("agent kernel cmdline: read failed: %v", err)
	}

	bootstrapGuestEnvironment(pid)

	startSSHAgentProxy()
	startGitHookForwarder()
	startDockerCredHelperListener()

	// Start the accept loop only after SSH-agent/git/docker listeners exist so
	// workspace.ready cannot win a race before /tmp/ssh-agent.sock is created.
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				emitDiagnostic("agent accept loop exited: %v", err)
				return
			}
			emitDiagnostic("agent accepted connection")
			go serveConn(conn)
		}
	}()

	// Bake mode: install the minimal daemon-readiness base layer synchronously,
	// then power off so the host can use the resulting rootfs as the pre-baked base.
	// Runs after the accept loop starts so the host can still reach the agent.
	if isBakeMode() {
		emitDiagnostic("agent bake: starting rootfs pre-bake")
		var bakeErr error
		if bakeErr = verifyBakeOutboundReachable(); bakeErr == nil {
			bakeErr = ensureGuestBasePackages()
		}
		if bakeErr != nil {
			emitDiagnostic("agent bake: FAILED — %v", bakeErr)
			// Stay alive briefly so the host can read the failure from the serial log.
			time.Sleep(5 * time.Second)
		} else {
			emitDiagnostic("agent bake: all tools installed — syncing filesystems before power off")
			if err := prepullBakedDockerImages(); err != nil {
				emitDiagnostic("agent bake: Docker image pre-pull failed (non-fatal): %v", err)
			}
			// Force dirty buffers to disk so the host sees all writes even if
			// libkrun's virtio-blk flush is lazy.
			_ = exec.Command("sync").Run()
			_ = exec.Command("sync").Run()
			_ = exec.Command("sync").Run()
			time.Sleep(2 * time.Second)
			emitDiagnostic("agent bake: powering off VM")
			// Give the serial console a moment to flush.
			time.Sleep(500 * time.Millisecond)
		}
		_ = listener.Close()
		_ = unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
		os.Exit(0) // unreachable, but satisfies the compiler
	}

	// Toolchain setup: in baked mode verify tools, otherwise run full install.
	if isBakedMode() {
		emitDiagnostic("agent base packages: baked mode — verifying tools in PATH")
		if toolchainPresentInPATH(os.Environ()) {
			emitDiagnostic("agent base packages: baked mode — all tools present")
		} else {
			emitDiagnostic("agent base packages: baked mode but tools missing — falling back to full install")
			if err := ensureGuestBasePackages(); err != nil {
				emitDiagnostic("agent base packages: FAILED — refusing to accept connections: %v", err)
				log.Fatalf("agent base packages: %v", err)
			}
		}
	} else if os.Getenv("NEXUS_SKIP_BASE_PACKAGES") == "1" {
		emitDiagnostic("agent base packages: NEXUS_SKIP_BASE_PACKAGES=1 — skipping installation (macOS pre-baked rootfs)")
	} else {
		// Legacy path: install the full base toolchain synchronously before accepting connections.
		if err := ensureGuestBasePackages(); err != nil {
			emitDiagnostic("agent base packages: FAILED — refusing to accept connections: %v", err)
			log.Fatalf("agent base packages: %v", err)
		}
	}
	// Ensure mise global node default is set on every boot, even in baked mode.
	// This is idempotent and fixes cases where the shim config was lost or never
	// created (e.g. after rootfs rebakes or stamp mismatches).
	agentEnv := ensurePathInEnv(os.Environ())
	if misePath, err := lookPathInEnv("mise", agentEnv); err == nil {
		if err := ensureMiseNodeGlobalDefault(misePath); err != nil {
			emitDiagnostic("agent mise global default: %v (non-fatal)", err)
		}
	}

	// Docker daemon starts in the background when this is the primary agent.
	if isPrimaryAgent() {
		go func() {
			if err := ensureDockerDaemon(); err != nil {
				emitDiagnostic("agent docker daemon setup failed (non-fatal): %v", err)
			}
		}()
	}
	go startSpotlightListener()

	emitDiagnostic("agent listener ready transport=%s", transport)
	log.Printf("nexus guest agent listening (%s)", transport)

	// Block until the accept loop exits (listener closed or fatal error).
	<-acceptDone
}

// isBakeMode reports whether the agent is running in rootfs-bake mode. In bake
// mode the agent installs all tools synchronously then powers off the VM so the
// host can use the resulting rootfs as the pre-baked base. In libkrun container
// mode the host signals bake mode via the NEXUS_BAKE env var.
func isBakeMode() bool {
	if os.Getenv("NEXUS_BAKE") == "1" {
		return true
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == "nexus.bake=1" {
			return true
		}
	}
	return false
}

// isBakedMode reports whether the host claims the rootfs was pre-baked. When
// true the agent skips the heavy apt-get/npm install path. In libkrun container
// mode the host signals baked mode via the NEXUS_BAKED env var.
func isBakedMode() bool {
	if os.Getenv("NEXUS_BAKED") == "1" {
		return true
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == "nexus.baked=1" {
			return true
		}
	}
	return false
}

// isPrimaryAgent reports whether this agent instance should behave like PID 1
// (mounting kernel filesystems, starting sshd, starting Docker). In virtiofs
// the agent IS PID 1. In libkrun container mode the host sets
// NEXUS_CONTAINER_MODE=1 to tell the agent to still perform these duties.
func isPrimaryAgent() bool {
	return os.Getpid() == 1 || os.Getenv("NEXUS_CONTAINER_MODE") == "1"
}

func emitDiagnostic(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	if console, err := os.OpenFile("/dev/console", os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_, _ = fmt.Fprintln(console, msg)
		_ = console.Close()
	}
	if kmsg, err := os.OpenFile("/dev/kmsg", os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_, _ = fmt.Fprintf(kmsg, "<6>nexus-guest-agent: %s\n", msg)
		_ = kmsg.Close()
	}
}
