//go:build linux

// Package libkrun implements a VM runtime driver using libkrun as the VMM.
// Unlike a separate hypervisor subprocess + REST API, libkrun is a shared library.
// VMs are run in child processes that call krun_start_enter() which takes
// over the process. The parent daemon communicates with VMs via Unix sockets
// that libkrun maps to vsock ports inside the guest.
package libkrun

// VMSpec holds the full configuration for a libkrun microVM child process.
// It is serialized as JSON and written to a temp file that the child reads.
//
// Guest disk layout (hybrid mode):
//
//	/dev/vda  rootfs.{raw,qcow2}     → /  (via krun_set_root_disk_remount)
//	/dev/vdb  workspace.ext4         → reserved workspace state volume
//	/dev/vdc  docker-data.ext4       → /var/lib/docker
//	/dev/vdd  hostconfig.ext4        → /run/nexus-host (optional, ro)
//	virtiofs "nexus-workspace"        → /workspace (rw)
type VMSpec struct {
	WorkspaceID string `json:"workspace_id"`
	// WorkspaceMode selects guest assembly path. Current production path is
	// hybrid (block rootfs + virtiofs workspace).
	WorkspaceMode string `json:"workspace_mode,omitempty"`

	// KernelPath points to the VM kernel image used by krun_set_kernel.
	KernelPath string `json:"kernel_path"`
	// KernelCmdline provides the kernel boot arguments.
	KernelCmdline string `json:"kernel_cmdline,omitempty"`

	// RootFSImage is the path to a block disk image used as the VM's root
	// filesystem. The launcher uses krun_set_root_disk_remount to pivot from
	// a dummy virtiofs init root to this block device. Supports raw (default)
	// and qcow2 (via RootFSImageFormat).
	RootFSImage string `json:"rootfs_image"`
	// RootFSImageFormat is the disk image format: 0=raw, 1=qcow2, 2=vmdk.
	// Defaults to 0 (raw) when empty.
	RootFSImageFormat int `json:"rootfs_image_format,omitempty"`

	// AgentPath is the absolute path inside the rootfs to the guest agent.
	AgentPath string `json:"agent_path"`

	// BakedRootfs tells the guest agent that the rootfs was pre-baked so it
	// can skip the heavy apt-get/npm install path.
	BakedRootfs bool `json:"baked_rootfs,omitempty"`

	// WorkspaceImage is the per-workspace ext4 image reserved for workspace
	// state/snapshots in hybrid mode.
	WorkspaceImage string `json:"workspace_image"`
	// WorkspaceHostPath is the daemon-host project path used for virtiofs share
	// in experimental workspace mode.
	WorkspaceHostPath string `json:"workspace_host_path,omitempty"`

	// DockerDataImage is a sparse ext4 image for Docker's data-root
	// (/var/lib/docker). Attached as /dev/vdc. Docker overlay2 requires a
	// native kernel filesystem; it cannot run on virtiofs.
	DockerDataImage string `json:"docker_data_image"`

	HostConfigDrive string `json:"host_config_drive,omitempty"`
	MemoryMiB       int    `json:"memory_mib"`
	VCPUs           int    `json:"vcpus"`
	SerialLog       string `json:"serial_log"`

	// SSHHostPort is the host-side TCP port that libkrun TSI maps to guest port 22.
	SSHHostPort int `json:"ssh_host_port,omitempty"`
	// NetworkBackend controls guest networking strategy:
	// "auto" (prefer virtio-net when available), "virtio-net", or "tsi".
	NetworkBackend string `json:"network_backend,omitempty"`
	// PortMap is a list of "hostPort:guestPort" TCP port-forward entries for krun_set_port_map.
	PortMap []string `json:"port_map,omitempty"`
	// PasstFDIndex is the index in the child process extra-files table for the
	// pre-opened passt unix-stream socket (effective FD = 3 + index).
	PasstFDIndex int            `json:"passt_fd_index,omitempty"`
	VsockPorts   []VsockPortCfg `json:"vsock_ports,omitempty"`

	// LibkrunLogLevel overrides the libkrun verbosity level (krun_set_log_level).
	// 0=off 1=error 2=warn 3=info 4=debug 5=trace. 0 means "use default" (4).
	LibkrunLogLevel int `json:"libkrun_log_level,omitempty"`
}

// VsockPortCfg maps a guest vsock port to a host Unix socket path.
//
//   - listen=true: Guest is the SERVER (listens on vsock port).
//     libkrun creates the Unix socket; the host daemon dials it.
//     Used for: agent exec (10789), spotlight (10792).
//
//   - listen=false: Host is the SERVER (listens on Unix socket).
//     libkrun proxies guest → Unix socket.
//     Used for: SSH agent proxy (10790).
type VsockPortCfg struct {
	Port   uint32 `json:"port"`
	Path   string `json:"path"`
	Listen bool   `json:"listen"`
}
