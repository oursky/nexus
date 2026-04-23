// Package libkrun implements a VM runtime driver using libkrun as the VMM.
// Unlike Firecracker (subprocess + REST API), libkrun is a shared library.
// VMs are run in child processes that call krun_start_enter() which takes
// over the process. The parent daemon communicates with VMs via Unix sockets
// that libkrun maps to vsock ports inside the guest.
package libkrun

// VMSpec holds the full configuration for a libkrun microVM child process.
// It is serialized as JSON and written to a temp file that the child reads.
type VMSpec struct {
	WorkspaceID     string         `json:"workspace_id"`
	KernelPath      string         `json:"kernel_path"`
	KernelCmdline   string         `json:"kernel_cmdline"`
	RootFSPath      string         `json:"rootfs_path"`
	WorkspaceImage  string         `json:"workspace_image"`
	BaseImage       string         `json:"base_image,omitempty"`
	OverlayMode     bool           `json:"overlay_mode,omitempty"`
	HostConfigDrive string         `json:"host_config_drive,omitempty"`
	MemoryMiB       int            `json:"memory_mib"`
	VCPUs           int            `json:"vcpus"`
	SerialLog       string         `json:"serial_log"`
	// PasstFDIndex is the index into exec.Cmd.ExtraFiles for the passt socket fd.
	// fd = 3 + PasstFDIndex (fd 0,1,2 = stdin,stdout,stderr; extras start at 3).
	PasstFDIndex    int            `json:"passt_fd_index"`
	SSHHostPort     int            `json:"ssh_host_port,omitempty"`
	VsockPorts      []VsockPortCfg `json:"vsock_ports,omitempty"`
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
