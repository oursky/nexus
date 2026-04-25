//go:build ignore

// Package libkrunvm is superseded by the standalone cmd/nexus-libkrun-vm binary.
// The main nexus daemon no longer links CGO against libkrun.so directly.
// This file is kept for historical reference only.
// The nexus daemon spawns this as a child process for each workspace VM.
// It reads a VMSpec JSON file, configures libkrun, and calls krun_start_enter()
// which takes over the process and runs the microVM. When the VM exits, the
// process exits.
package libkrunvm

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
)

// Command returns the hidden "libkrun-vm" cobra command.
func Command() *cobra.Command {
	var configFile string

	cmd := &cobra.Command{
		Use:    "libkrun-vm",
		Short:  "Run a libkrun microVM (internal: spawned by daemon)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVM(configFile)
		},
	}

	cmd.Flags().StringVar(&configFile, "config", "", "path to VMSpec JSON file")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

// runVM reads the VMSpec from configFile, configures libkrun, and starts the VM.
// This function does not return on success — the process becomes the VMM.
func runVM(configFile string) error {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("libkrun-vm: read config: %w", err)
	}

	var spec libkrun.VMSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("libkrun-vm: parse config: %w", err)
	}

	return launchVM(spec)
}

// launchVM configures libkrun from spec and calls krun_start_enter.
// Does not return on success.
func launchVM(spec libkrun.VMSpec) error {
	logf := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "[libkrun-vm] "+format+"\n", args...)
	}

	// Enable libkrun debug logging to stderr.
	krunSetLogLevel(4)

	logf("creating context for workspace %s", spec.WorkspaceID)
	ctx, err := krunCreate()
	if err != nil {
		return fmt.Errorf("libkrun-vm: %w", err)
	}
	logf("ctx=%d", ctx)

	// Enable verbose libkrun logging for diagnostics.
	// krun_set_log_level: 0=off 1=error 2=warn 3=info 4=debug
	// (optional; ignore error — not critical)

	// vCPUs and RAM
	vcpus := spec.VCPUs
	if vcpus < 1 {
		vcpus = 1
	}
	memMiB := spec.MemoryMiB
	if memMiB < 256 {
		memMiB = 512
	}
	logf("set_vm_config: vcpus=%d mem=%d MiB", vcpus, memMiB)
	if err := krunSetVMConfig(ctx, uint8(vcpus), uint32(memMiB)); err != nil {
		return fmt.Errorf("libkrun-vm: %w", err)
	}

	// Custom kernel (same vmlinux image used for libkrun guests).
	// Our vmlinux.bin is a statically-linked ELF kernel.
	logf("set_kernel: path=%s cmdline=%q", spec.KernelPath, spec.KernelCmdline)
	if err := krunSetKernel(ctx, spec.KernelPath, "", spec.KernelCmdline, 1 /* KRUN_KERNEL_FORMAT_ELF */); err != nil {
		// Try gzip-compressed image as fallback (bzImage).
		logf("set_kernel ELF failed (%v), trying IMAGE_GZ", err)
		if err2 := krunSetKernel(ctx, spec.KernelPath, "", spec.KernelCmdline, 4 /* KRUN_KERNEL_FORMAT_IMAGE_GZ */); err2 != nil {
			return fmt.Errorf("libkrun-vm: set kernel (tried elf=%v gz=%v)", err, err2)
		}
	}

	// Root filesystem (rootfs.ext4 for libkrun guests).
	logf("add_disk: rootfs=%s", spec.RootFSPath)
	if err := krunAddDisk(ctx, "rootfs", spec.RootFSPath, false); err != nil {
		return fmt.Errorf("libkrun-vm: add rootfs disk: %w", err)
	}

	// Workspace image (writable, mounted at /workspace in the guest).
	if spec.OverlayMode {
		if spec.BaseImage != "" {
			logf("add_disk: workspace_base=%s", spec.BaseImage)
			if err := krunAddDisk(ctx, "workspace_base", spec.BaseImage, true); err != nil {
				return fmt.Errorf("libkrun-vm: add base disk: %w", err)
			}
		}
		logf("add_disk: workspace_overlay=%s", spec.WorkspaceImage)
		if err := krunAddDisk(ctx, "workspace_overlay", spec.WorkspaceImage, false); err != nil {
			return fmt.Errorf("libkrun-vm: add overlay disk: %w", err)
		}
	} else {
		logf("add_disk: workspace=%s", spec.WorkspaceImage)
		if err := krunAddDisk(ctx, "workspace", spec.WorkspaceImage, false); err != nil {
			return fmt.Errorf("libkrun-vm: add workspace disk: %w", err)
		}
	}

	// Host config drive.
	if spec.HostConfigDrive != "" {
		logf("add_disk: hostconfig=%s", spec.HostConfigDrive)
		if err := krunAddDisk(ctx, "hostconfig", spec.HostConfigDrive, true); err != nil {
			logf("warning: host config drive: %v", err)
		}
	}

	// Networking: passt socket via krun_set_passt_fd (simpler API).
	// The parent process started passt and passed the VM-side socket fd as ExtraFile[PasstFDIndex].
	passtFD := 3 + spec.PasstFDIndex
	logf("set_passt_fd: fd=%d", passtFD)
	if err := krunSetPasstFD(ctx, passtFD); err != nil {
		logf("krun_set_passt_fd failed (%v), trying krun_add_net_unixstream", err)
		if err2 := krunAddNetUnixStream(ctx, passtFD); err2 != nil {
			return fmt.Errorf("libkrun-vm: network setup: passt_fd=%v unixstream=%v", err, err2)
		}
	}

	// Vsock port mappings.
	for _, vp := range spec.VsockPorts {
		logf("add_vsock_port2: port=%d path=%s listen=%v", vp.Port, vp.Path, vp.Listen)
		if err := krunAddVsockPort2(ctx, vp.Port, vp.Path, vp.Listen); err != nil {
			return fmt.Errorf("libkrun-vm: vsock port %d: %w", vp.Port, err)
		}
	}

	// Add an explicit serial console (ttyS0) pointing to our debug log (fd 1 = stdout).
	// This captures early kernel boot messages which appear before VirtIO console (hvc0) starts.
	// NOTE: krun_add_serial_console_default on x86 Linux does NOT require disabling the implicit
	// console first — it adds ttyS0 as the FIRST serial device (irrespective of hvc0).
	logf("add_serial_console_default: input=/dev/null output=stdout(1)")
	if err := krunAddSerialConsoleDefault(ctx, -1, 1); err != nil {
		logf("warning: serial console: %v (continuing with implicit hvc0 only)", err)
	}

	// Also redirect implicit VirtIO console (hvc0) output to a separate file.
	if spec.SerialLog != "" {
		serialLogPath := spec.SerialLog + ".hvc0"
		logf("set_console_output: %s", serialLogPath)
		if err := krunSetConsoleOutput(ctx, serialLogPath); err != nil {
			logf("warning: set hvc0 console output: %v", err)
		}
	}

	logf("calling krun_start_enter (process will become the VMM)")

	// This call does not return on success.
	return krunStartEnter(ctx)
}
