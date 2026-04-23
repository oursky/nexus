//go:build linux && libkrun

// Package libkrunvm implements the hidden "libkrun-vm" subcommand.
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
	ctx, err := krunCreate()
	if err != nil {
		return fmt.Errorf("libkrun-vm: %w", err)
	}

	// vCPUs and RAM
	vcpus := spec.VCPUs
	if vcpus < 1 {
		vcpus = 1
	}
	memMiB := spec.MemoryMiB
	if memMiB < 256 {
		memMiB = 512
	}
	if err := krunSetVMConfig(ctx, uint8(vcpus), uint32(memMiB)); err != nil {
		return fmt.Errorf("libkrun-vm: %w", err)
	}

	// Custom kernel (same kernel used by Firecracker).
	// Format: ELF (1) for uncompressed vmlinux, BZ2 (3) for bzImage, GZ (4) for gzip.
	// Our kernel binary is typically a gzip-compressed bzImage.
	if err := krunSetKernel(ctx, spec.KernelPath, "", spec.KernelCmdline, 4 /* KRUN_KERNEL_FORMAT_IMAGE_GZ */); err != nil {
		// Try ELF format as fallback
		if err2 := krunSetKernel(ctx, spec.KernelPath, "", spec.KernelCmdline, 1 /* KRUN_KERNEL_FORMAT_ELF */); err2 != nil {
			return fmt.Errorf("libkrun-vm: set kernel (tried gz and elf): gz=%v elf=%v", err, err2)
		}
	}

	// Root filesystem (same rootfs.ext4 used by Firecracker).
	if err := krunAddDisk(ctx, "rootfs", spec.RootFSPath, false); err != nil {
		return fmt.Errorf("libkrun-vm: add rootfs disk: %w", err)
	}

	// Workspace image (writable, mounted at /workspace in the guest).
	if spec.OverlayMode {
		// Overlay mode: read-only base + writable overlay layer.
		if spec.BaseImage != "" {
			if err := krunAddDisk(ctx, "workspace_base", spec.BaseImage, true); err != nil {
				return fmt.Errorf("libkrun-vm: add base disk: %w", err)
			}
		}
		if err := krunAddDisk(ctx, "workspace_overlay", spec.WorkspaceImage, false); err != nil {
			return fmt.Errorf("libkrun-vm: add overlay disk: %w", err)
		}
	} else {
		// Legacy mode: full writable workspace image.
		if err := krunAddDisk(ctx, "workspace", spec.WorkspaceImage, false); err != nil {
			return fmt.Errorf("libkrun-vm: add workspace disk: %w", err)
		}
	}

	// Host config drive (gitconfig, SSH keys, tool auth files).
	if spec.HostConfigDrive != "" {
		if err := krunAddDisk(ctx, "hostconfig", spec.HostConfigDrive, true); err != nil {
			// Non-fatal: log and continue without config drive.
			fmt.Fprintf(os.Stderr, "[libkrun-vm] warning: host config drive: %v\n", err)
		}
	}

	// Networking: passt socket passed via extra fd (fd 3 + PasstFDIndex).
	// The parent process set up the socket pair and started passt with one end.
	passtFD := 3 + spec.PasstFDIndex
	if err := krunAddNetUnixStream(ctx, passtFD); err != nil {
		return fmt.Errorf("libkrun-vm: attach passt network: %w", err)
	}

	// Vsock port mappings for agent communication.
	for _, vp := range spec.VsockPorts {
		if err := krunAddVsockPort2(ctx, vp.Port, vp.Path, vp.Listen); err != nil {
			return fmt.Errorf("libkrun-vm: vsock port %d: %w", vp.Port, err)
		}
	}

	// Console output → log file.
	if spec.SerialLog != "" {
		if err := krunSetConsoleOutput(ctx, spec.SerialLog); err != nil {
			fmt.Fprintf(os.Stderr, "[libkrun-vm] warning: set console output: %v\n", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[libkrun-vm] starting VM for workspace %s\n", spec.WorkspaceID)

	// This call does not return on success.
	return krunStartEnter(ctx)
}
