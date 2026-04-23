// Package libkrun implements a VM runtime driver using libkrun as the VMM.
// Unlike Firecracker (subprocess + REST API), libkrun is a shared library.
// VMs are run in child processes that call krun_start_enter() which takes
// over the process. The parent daemon communicates with VMs via Unix sockets
// that libkrun maps to vsock ports inside the guest.
package libkrun

// VMSpec holds the full configuration for a libkrun microVM child process.
// It is serialized as JSON and written to a temp file that the child reads.
//
// Hybrid mode (block root + block workspace):
//
//	krun_set_kernel(NULL, cmdline)      embedded libkrunfw kernel
//	/dev/vda  rootfs.ext4              writable VM root filesystem
//	/dev/vdb  workspace.ext4           writable workspace filesystem
//	/dev/vdc  docker-data.ext4         sparse, Docker overlay2 data-root
//	/dev/vdd  hostconfig.ext4          read-only, SSH keys + API tokens
//
// Each workspace gets its own reflink/sparse-cloned rootfs image so "root"
// behaves like a real VM root filesystem while forks stay space-efficient.
type VMSpec struct {
	WorkspaceID string `json:"workspace_id"`

	// KernelPath points to the VM kernel image used by krun_set_kernel.
	KernelPath string `json:"kernel_path"`
	// KernelCmdline provides the kernel boot arguments.
	KernelCmdline string `json:"kernel_cmdline,omitempty"`

	// RootFSImage is the per-workspace writable rootfs ext4 attached as /dev/vda.
	RootFSImage string `json:"rootfs_image"`

	// AgentPath is the absolute path inside the rootfs to the guest agent.
	AgentPath string `json:"agent_path"`

	// WorkspaceImage is the per-workspace ext4 image mounted at /workspace.
	WorkspaceImage string `json:"workspace_image"`

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
