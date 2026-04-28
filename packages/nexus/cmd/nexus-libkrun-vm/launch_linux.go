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
func launchVM(spec libkrun.VMSpec) error {
	logf := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "[libkrun-vm] "+format+"\n", args...)
	}

	logLevel := uint32(4) // default: debug
	if spec.LibkrunLogLevel > 0 {
		logLevel = uint32(spec.LibkrunLogLevel)
	}
	krunSetLogLevel(logLevel)

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

	rootfsImage := strings.TrimSpace(spec.RootFSImage)
	workspaceHostPath := strings.TrimSpace(spec.WorkspaceHostPath)

	if rootfsImage == "" {
		return fmt.Errorf("launchVM requires rootfs_image")
	}
	if workspaceHostPath != "" {
		return launchHybridMode(ctx, spec, logf)
	}
	return launchBlockMode(ctx, spec, logf)
}

func setKernel(ctx uint32, spec libkrun.VMSpec, logf func(string, ...interface{})) error {
	kernelPath := strings.TrimSpace(spec.KernelPath)
	if kernelPath == "" {
		return fmt.Errorf("kernel path is required")
	}

	// Detect kernel image format from file magic so we pass the correct
	// krun_set_kernel format code. libkrun validates the format at VM start
	// time (krun_start_enter), so passing the wrong format produces a cryptic
	// ElfLoadKernel(Elf(InvalidElfMagicNumber)) error instead of a clean
	// early failure.
	//
	// libkrun format constants (from libkrun.h):
	//   0 = RAW, 1 = ELF, 2 = PE_GZ, 3 = BZ2, 4 = GZ, 5 = ZSTD
	format, detected := detectKernelFormat(kernelPath)
	name := kernelFormatName(format)
	if detected {
		logf("set_kernel: detected format=%s from magic", name)
	} else {
		logf("set_kernel: unknown magic, defaulting to format=%s", name)
	}

	logf("set_kernel: path=%s format=%s cmdline=%q", kernelPath, name, spec.KernelCmdline)
	if err := krunSetKernel(ctx, kernelPath, "", spec.KernelCmdline, format); err != nil {
		// If the detected format failed, try other formats as fallback.
		fallbacks := fallbackFormats(format)
		for _, fb := range fallbacks {
			fbName := kernelFormatName(fb)
			logf("set_kernel format=%s failed (%v), trying format=%s", name, err, fbName)
			if err2 := krunSetKernel(ctx, kernelPath, "", spec.KernelCmdline, fb); err2 == nil {
				return nil
			}
		}
		return fmt.Errorf("set kernel: tried format=%s and fallbacks, all failed (first err: %w)", name, err)
	}
	return nil
}

// kernelFormatName returns a human-readable name for a libkrun kernel format constant.
func kernelFormatName(format uint32) string {
	switch format {
	case 0:
		return "raw"
	case 1:
		return "elf"
	case 2:
		return "pe_gz"
	case 3:
		return "bz2"
	case 4:
		return "gz"
	case 5:
		return "zstd"
	default:
		return fmt.Sprintf("%d", format)
	}
}

// fallbackFormats returns a list of formats to try when the detected format fails.
func fallbackFormats(detected uint32) []uint32 {
	// Order: raw, elf, gz, zstd, bz2, pe_gz
	candidates := []uint32{0, 1, 4, 5, 3, 2}
	var out []uint32
	for _, c := range candidates {
		if c != detected {
			out = append(out, c)
		}
	}
	return out
}

// detectKernelFormat reads the first bytes of a kernel image and returns the
// best-guess libkrun format constant plus a bool indicating whether the magic
// was recognized. When unrecognized, it defaults to RAW (0) since many kernel
// binaries (especially ARM64 Image/vmlinux.bin) are raw flat binaries.
func detectKernelFormat(path string) (uint32, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	buf := make([]byte, 16)
	n, err := f.Read(buf)
	if err != nil || n < 2 {
		return 0, false
	}
	switch {
	// ELF: 0x7f ELF
	case n >= 4 && buf[0] == 0x7f && buf[1] == 'E' && buf[2] == 'L' && buf[3] == 'F':
		return 1, true // KRUN_KERNEL_FORMAT_ELF
	// GZIP: 0x1f 0x8b
	case buf[0] == 0x1f && buf[1] == 0x8b:
		return 4, true // KRUN_KERNEL_FORMAT_IMAGE_GZ
	// ZSTD: 0x28 0xb5 0x2f 0xfd
	case n >= 4 && buf[0] == 0x28 && buf[1] == 0xb5 && buf[2] == 0x2f && buf[3] == 0xfd:
		return 5, true // KRUN_KERNEL_FORMAT_IMAGE_ZSTD
	// BZ2: "BZh"
	case n >= 3 && buf[0] == 'B' && buf[1] == 'Z' && buf[2] == 'h':
		return 3, true // KRUN_KERNEL_FORMAT_IMAGE_BZ2
	// XZ: 0xfd 0x37 0x7a 0x58 0x5a 0x00
	case n >= 6 && buf[0] == 0xfd && buf[1] == 0x37 && buf[2] == 0x7a && buf[3] == 0x58 && buf[4] == 0x5a && buf[5] == 0x00:
		// libkrun doesn't have a dedicated XZ format; try raw and hope.
		return 0, true
	default:
		return 0, false // default to RAW (unknown magic)
	}
}

// launchHybridMode boots a VM with a block rootfs and virtiofs workspace share.
// The root filesystem is a block device (giving the guest true root ownership),
// while /workspace is still shared via virtiofs for the volume mount feature.
//
// Guest disk layout:
//
//	/dev/vda  rootfs.{raw,qcow2}     → /  (via krun_set_root_disk_remount)
//	/dev/vdb  workspace.ext4         → overlay upper for /workspace
//	/dev/vdc  docker-data.ext4       → /var/lib/docker
//	/dev/vdd  hostconfig.ext4        → /run/nexus-host (optional, ro)
//	virtiofs "nexus-workspace"        → project dir (ro lower layer)
func launchHybridMode(ctx uint32, spec libkrun.VMSpec, logf func(string, ...interface{})) error {
	rootfsPath := strings.TrimSpace(spec.RootFSImage)
	if rootfsPath == "" {
		return fmt.Errorf("hybrid mode requires rootfs_image")
	}
	if _, err := os.Stat(rootfsPath); err != nil {
		return fmt.Errorf("hybrid mode rootfs image stat: %w", err)
	}

	format := uint32(spec.RootFSImageFormat)
	if format == 0 {
		format = 0 // raw
	}
	logf("add_disk: rootfs=%s format=%d", rootfsPath, format)
	if err := krunAddDiskWithFormat(ctx, "rootfs", rootfsPath, format, false); err != nil {
		return fmt.Errorf("add rootfs disk: %w", err)
	}

	logf("add_disk: workspace=%s", spec.WorkspaceImage)
	if err := krunAddDisk(ctx, "workspace", spec.WorkspaceImage, false); err != nil {
		return fmt.Errorf("add workspace disk: %w", err)
	}
	logf("add_disk: docker_data=%s", spec.DockerDataImage)
	if err := krunAddDisk(ctx, "docker_data", spec.DockerDataImage, false); err != nil {
		return fmt.Errorf("add docker-data disk: %w", err)
	}
	if spec.HostConfigDrive != "" {
		logf("add_disk: hostconfig=%s", spec.HostConfigDrive)
		if err := krunAddDisk(ctx, "hostconfig", spec.HostConfigDrive, true); err != nil {
			logf("warning: host config drive: %v", err)
		}
	}

	logf("set_root_disk_remount: /dev/vda (ext4)")
	if err := krunSetRootDiskRemount(ctx, "/dev/vda", "ext4", ""); err != nil {
		return fmt.Errorf("set root disk remount: %w", err)
	}

	hostPath := strings.TrimSpace(spec.WorkspaceHostPath)
	if hostPath == "" {
		return fmt.Errorf("hybrid mode requires workspace_host_path")
	}
	if fi, err := os.Stat(hostPath); err != nil || !fi.IsDir() {
		if err != nil {
			return fmt.Errorf("hybrid workspace_host_path stat: %w", err)
		}
		return fmt.Errorf("hybrid workspace_host_path is not a directory: %s", hostPath)
	}
	logf("add_virtiofs: tag=nexus-workspace path=%s ro=true", hostPath)
	if err := krunAddVirtioFS3(ctx, "nexus-workspace", hostPath, 0, true); err != nil {
		return fmt.Errorf("add virtiofs workspace share: %w", err)
	}

	if strings.TrimSpace(spec.KernelPath) != "" {
		if err := setKernel(ctx, spec, logf); err != nil {
			return err
		}
	}

	if err := configureNetworking(ctx, spec, logf); err != nil {
		return err
	}
	if err := addVsockAndConsole(ctx, spec, logf); err != nil {
		return err
	}

	agentPath := strings.TrimSpace(spec.AgentPath)
	if agentPath == "" {
		agentPath = "/usr/local/bin/nexus-guest-agent"
	}
	env := []string{
		"NEXUS_CONTAINER_MODE=1",
		"NEXUS_WORKSPACE_MODE=virtiofs",
		"NEXUS_OVERLAY_DEV=/dev/vdb",
		"NEXUS_DOCKER_DEV=/dev/vdc",
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
	}
	if spec.BakedRootfs {
		env = append(env, "NEXUS_BAKED=1")
	}
	if strings.Contains(spec.KernelCmdline, "nexus.bake=1") {
		env = append(env, "NEXUS_BAKE=1")
	}
	if spec.HostConfigDrive != "" {
		env = append(env, "NEXUS_CONFIG_DEV=/dev/vdd")
	}
	if err := krunSetWorkdir(ctx, "/"); err != nil {
		return fmt.Errorf("set workdir: %w", err)
	}
	logf("set_exec: path=%s env=%d vars", agentPath, len(env))
	if err := krunSetExec(ctx, agentPath, env); err != nil {
		return fmt.Errorf("set exec: %w", err)
	}

	logf("calling krun_start_enter (process will become the VMM)")
	return krunStartEnter(ctx)
}

// launchBlockMode boots a VM with the rootfs as a block device (ext4 disk).
// Used for the bake VM where virtiofs ownership restrictions break apt-get.
func launchBlockMode(ctx uint32, spec libkrun.VMSpec, logf func(string, ...interface{})) error {
	if err := setKernel(ctx, spec, logf); err != nil {
		return err
	}

	rootfsPath := strings.TrimSpace(spec.RootFSImage)
	if rootfsPath == "" {
		return fmt.Errorf("block mode requires rootfs_image")
	}
	if _, err := os.Stat(rootfsPath); err != nil {
		return fmt.Errorf("block mode rootfs image stat: %w", err)
	}
	logf("add_disk: rootfs=%s", rootfsPath)
	if err := krunAddDisk(ctx, "rootfs", rootfsPath, false); err != nil {
		return fmt.Errorf("add rootfs disk: %w", err)
	}

	if spec.WorkspaceImage != "" {
		logf("add_disk: workspace=%s", spec.WorkspaceImage)
		if err := krunAddDisk(ctx, "workspace", spec.WorkspaceImage, false); err != nil {
			return fmt.Errorf("add workspace disk: %w", err)
		}
	}
	if spec.DockerDataImage != "" {
		logf("add_disk: docker_data=%s", spec.DockerDataImage)
		if err := krunAddDisk(ctx, "docker_data", spec.DockerDataImage, false); err != nil {
			return fmt.Errorf("add docker-data disk: %w", err)
		}
	}
	if spec.HostConfigDrive != "" {
		logf("add_disk: hostconfig=%s", spec.HostConfigDrive)
		if err := krunAddDisk(ctx, "hostconfig", spec.HostConfigDrive, true); err != nil {
			logf("warning: host config drive: %v", err)
		}
	}

	if err := configureNetworking(ctx, spec, logf); err != nil {
		return err
	}
	if err := addVsockAndConsole(ctx, spec, logf); err != nil {
		return err
	}

	agentPath := strings.TrimSpace(spec.AgentPath)
	if agentPath == "" {
		agentPath = "/usr/local/bin/nexus-guest-agent"
	}
	env := []string{
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
	}
	if spec.BakedRootfs {
		env = append(env, "NEXUS_BAKED=1")
	}
	if strings.Contains(spec.KernelCmdline, "nexus.bake=1") {
		env = append(env, "NEXUS_BAKE=1")
	}
	if spec.HostConfigDrive != "" {
		env = append(env, "NEXUS_CONFIG_DEV=/dev/vdb")
	}
	if err := krunSetWorkdir(ctx, "/"); err != nil {
		return fmt.Errorf("set workdir: %w", err)
	}
	logf("set_exec: path=%s env=%d vars", agentPath, len(env))
	if err := krunSetExec(ctx, agentPath, env); err != nil {
		return fmt.Errorf("set exec: %w", err)
	}

	logf("calling krun_start_enter (process will become the VMM)")
	return krunStartEnter(ctx)
}

func configureNetworking(ctx uint32, spec libkrun.VMSpec, logf func(string, ...interface{})) error {
	const tsiFeatureHijackINET = 1 << 0

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
		if err := krunDisableImplicitVSock(ctx); err != nil {
			return fmt.Errorf("disable implicit vsock for tsi: %w", err)
		}
		if err := krunAddVSock(ctx, tsiFeatureHijackINET); err != nil {
			return fmt.Errorf("enable tsi vsock features: %w", err)
		}
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
			logf("network backend=auto->tsi (libkrun lacks krun_add_net_unixstream)")
			if err := krunDisableImplicitVSock(ctx); err != nil {
				return fmt.Errorf("disable implicit vsock for tsi: %w", err)
			}
			if err := krunAddVSock(ctx, tsiFeatureHijackINET); err != nil {
				return fmt.Errorf("enable tsi vsock features: %w", err)
			}
			if err := krunSetPortMap(ctx, spec.PortMap); err != nil {
				return fmt.Errorf("set port map: %w", err)
			}
		}
	default:
		return fmt.Errorf("unsupported network backend %q", backend)
	}
	return nil
}

func addVsockAndConsole(ctx uint32, spec libkrun.VMSpec, logf func(string, ...interface{})) error {
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
	return nil
}
