//go:build linux && libkrun

package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"
)

func passtMACForWorkspace(workspaceID string) [6]byte {
	h := fnv.New32a()
	_, _ = h.Write([]byte(workspaceID))
	v := h.Sum32()
	// Locally administered unicast MAC.
	return [6]byte{0x02, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v), 0x01}
}

// launchVM configures libkrun from spec and calls krun_start_enter.
// Does not return on success — the process becomes the VMM.
//
// Block-image mode (current architecture):
//
//	krun_set_kernel(kernel, cmdline) external kernel image
//	/dev/vda rootfs.ext4            writable block root filesystem
//	/dev/vdb workspace.ext4         writable workspace filesystem
//	/dev/vdc docker-data            Docker overlay2 data-root
//	/dev/vdd hostconfig             SSH keys, API tokens (read-only)
func launchVM(spec libkrun.VMSpec) error {
	logf := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "[libkrun-vm] "+format+"\n", args...)
	}

	krunSetLogLevel(4)

	logf("creating context for workspace %s", spec.WorkspaceID)
	ctx, err := krunCreate()
	if err != nil {
		return fmt.Errorf("create ctx: %w", err)
	}

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
		return err
	}

	// Configure explicit kernel image + cmdline.
	logf("set_kernel: path=%s cmdline=%q", spec.KernelPath, spec.KernelCmdline)
	if err := krunSetKernel(ctx, spec.KernelPath, "", spec.KernelCmdline, 1 /* KRUN_KERNEL_FORMAT_ELF */); err != nil {
		// Try gzip-compressed image as fallback (bzImage).
		logf("set_kernel ELF failed (%v), trying IMAGE_GZ", err)
		if err2 := krunSetKernel(ctx, spec.KernelPath, "", spec.KernelCmdline, 4 /* KRUN_KERNEL_FORMAT_IMAGE_GZ */); err2 != nil {
			return fmt.Errorf("set kernel (tried elf=%v gz=%v)", err, err2)
		}
	}

	// /dev/vda: writable root filesystem.
	logf("add_disk: rootfs=%s", spec.RootFSImage)
	if err := krunAddDisk(ctx, "rootfs", spec.RootFSImage, false); err != nil {
		return fmt.Errorf("add rootfs disk: %w", err)
	}

	// /dev/vdb: workspace image mounted at /workspace.
	logf("add_disk: workspace=%s", spec.WorkspaceImage)
	if err := krunAddDisk(ctx, "workspace", spec.WorkspaceImage, false); err != nil {
		return fmt.Errorf("add workspace disk: %w", err)
	}

	// /dev/vdc: Docker data-root (native ext4 required by overlay2 storage driver).
	logf("add_disk: docker_data=%s", spec.DockerDataImage)
	if err := krunAddDisk(ctx, "docker_data", spec.DockerDataImage, false); err != nil {
		return fmt.Errorf("add docker-data disk: %w", err)
	}

	// /dev/vdd: host config drive (SSH keys, git config, API tokens) — read-only.
	if spec.HostConfigDrive != "" {
		logf("add_disk: hostconfig=%s", spec.HostConfigDrive)
		if err := krunAddDisk(ctx, "hostconfig", spec.HostConfigDrive, true); err != nil {
			logf("warning: host config drive: %v", err)
		}
	}

	backend := strings.TrimSpace(spec.NetworkBackend)
	if backend == "" {
		backend = "auto"
	}
	switch backend {
	case "virtio-net":
		if !krunHasSymbol("krun_add_net_unixstream") {
			return fmt.Errorf("virtio-net requested, but libkrun does not export krun_add_net_unixstream; update libkrun or set NEXUS_LIBKRUN_NET_BACKEND=tsi")
		}
		passtFD := 3 + spec.PasstFDIndex
		mac := passtMACForWorkspace(spec.WorkspaceID)
		logf("network backend=virtio-net fd=%d mac=%02x:%02x:%02x:%02x:%02x:%02x", passtFD, mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
		if err := krunAddNetUnixStream(ctx, passtFD, mac); err != nil {
			return fmt.Errorf("add virtio-net unixstream: %w", err)
		}
	case "tsi":
		logf("network backend=tsi set_port_map=%v", spec.PortMap)
		if err := krunSetPortMap(ctx, spec.PortMap); err != nil {
			return fmt.Errorf("set port map: %w", err)
		}
	case "auto":
		if krunHasSymbol("krun_add_net_unixstream") {
			passtFD := 3 + spec.PasstFDIndex
			mac := passtMACForWorkspace(spec.WorkspaceID)
			logf("network backend=auto->virtio-net fd=%d mac=%02x:%02x:%02x:%02x:%02x:%02x", passtFD, mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
			if err := krunAddNetUnixStream(ctx, passtFD, mac); err != nil {
				return fmt.Errorf("add virtio-net unixstream: %w", err)
			}
		} else {
			logf("network backend=auto->tsi (libkrun lacks krun_add_net_unixstream); outbound network may be limited in VM mode")
			if err := krunSetPortMap(ctx, spec.PortMap); err != nil {
				return fmt.Errorf("set port map: %w", err)
			}
		}
	default:
		return fmt.Errorf("unsupported network backend %q", backend)
	}

	for _, vp := range spec.VsockPorts {
		logf("add_vsock_port2: port=%d path=%s listen=%v", vp.Port, vp.Path, vp.Listen)
		if err := krunAddVsockPort2(ctx, vp.Port, vp.Path, vp.Listen); err != nil {
			return fmt.Errorf("vsock port %d: %w", vp.Port, err)
		}
	}

	logf("add_serial_console_default: input=/dev/null output=stdout(1)")
	if err := krunAddSerialConsoleDefault(ctx, -1, 1); err != nil {
		logf("warning: serial console: %v (continuing)", err)
	}

	if spec.SerialLog != "" {
		serialLogPath := spec.SerialLog + ".hvc0"
		logf("set_console_output: %s", serialLogPath)
		if err := krunSetConsoleOutput(ctx, serialLogPath); err != nil {
			logf("warning: set hvc0 console output: %v", err)
		}
	}

	logf("calling krun_start_enter (process will become the VMM)")
	return krunStartEnter(ctx)
}
