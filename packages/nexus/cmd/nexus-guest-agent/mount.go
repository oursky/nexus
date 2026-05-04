//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var (
	setupWorkspaceMountFunc         = setupWorkspaceMount
	setupWorkspaceMountRequiredFunc = setupWorkspaceMountRequired
	workspaceMountPoint             = "/workspace"
	workspaceDevicePath             = "/dev/vdb"
	workspaceDeviceAttempts         = 300
	workspaceDeviceInterval         = 100 * time.Millisecond
	workspaceMkdirAll               = os.MkdirAll
	workspaceStat                   = os.Stat
	workspaceMountFunc              = unix.Mount
	workspaceUnmountFunc            = unix.Unmount
	workspaceReadProcMounts         = os.ReadFile
	kernelMkdirAll                  = os.MkdirAll
	kernelMountFunc                 = unix.Mount

	// virtiofsWorkspaceOnce ensures setupVirtiofsWorkspace runs exactly once
	// during agent lifecycle.
	virtiofsWorkspaceOnce    sync.Once
	virtiofsWorkspaceOnceErr error
)

// isVirtiofsWorkspaceMode reports whether the workspace uses virtiofs.
// In libkrun container mode the kernel cmdline is not under our control, so
// the host sets NEXUS_WORKSPACE_MODE=virtiofs via krun_set_exec. Legacy
// virtiofs guests set nexus.workspace=virtiofs on the kernel cmdline.
func isVirtiofsWorkspaceMode() bool {
	if os.Getenv("NEXUS_WORKSPACE_MODE") == "virtiofs" {
		return true
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == "nexus.workspace=virtiofs" {
			return true
		}
	}
	return false
}

// dockerDevPath returns the block device for docker-data.
// Default /dev/vdc (virtiofs mode); overridden in libkrun via NEXUS_DOCKER_DEV.
func dockerDevPath() string {
	if v := os.Getenv("NEXUS_DOCKER_DEV"); v != "" {
		return v
	}
	return "/dev/vdc"
}

// configDevPath returns the block device for the host config drive.
// Default /dev/vdd (virtiofs mode); overridden in libkrun via NEXUS_CONFIG_DEV.
func configDevPath() string {
	if v := os.Getenv("NEXUS_CONFIG_DEV"); v != "" {
		return v
	}
	return "/dev/vdd"
}

// setupVirtiofsWorkspace assembles the virtiofs workspace mount.
//
// Device layout is resolved from environment variables set by the host daemon:
//
//	virtiofs "nexus-workspace"   project dir (rw at /workspace)
//	NEXUS_DOCKER_DEV  (/dev/vdc) docker-data.ext4 → /var/lib/docker
//	NEXUS_CONFIG_DEV  (/dev/vdd) hostconfig.ext4  → /run/nexus-host  (read-only)
//
// virtiofs mode falls back to /dev/vdc, /dev/vdd.
func setupVirtiofsWorkspace() error {
	dockerDev := dockerDevPath()

	for _, dir := range []string{workspaceMountPoint, "/var/lib/docker"} {
		if err := workspaceMkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Mount virtiofs tag directly at /workspace as writable.
	if err := workspaceMountFunc("nexus-workspace", workspaceMountPoint, "virtiofs", 0, ""); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount virtiofs nexus-workspace → %s: %w", workspaceMountPoint, err)
		}
	} else {
		emitDiagnostic("agent virtiofs workspace mounted at %s (rw)", workspaceMountPoint)
	}

	// Wait for docker-data ext4 device and mount to /var/lib/docker.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := workspaceStat(dockerDev); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("docker-data device %s not available after 30s", dockerDev)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := workspaceMountFunc(dockerDev, "/var/lib/docker", "ext4", 0, ""); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount docker-data %s → /var/lib/docker: %w", dockerDev, err)
		}
	} else {
		emitDiagnostic("agent docker-data mounted at /var/lib/docker")
	}

	return nil
}

func setupWorkspaceMount() error {
	if isVirtiofsWorkspaceMode() {
		return setupVirtiofsWorkspaceOnce()
	}
	return setupBlockWorkspaceMount()
}

func setupWorkspaceMountRequired() error {
	if isVirtiofsWorkspaceMode() {
		return setupVirtiofsWorkspaceOnce()
	}
	return setupBlockWorkspaceMount()
}

// setupVirtiofsWorkspaceOnce calls setupVirtiofsWorkspace exactly once.
func setupVirtiofsWorkspaceOnce() error {
	virtiofsWorkspaceOnce.Do(func() {
		virtiofsWorkspaceOnceErr = setupVirtiofsWorkspace()
	})
	return virtiofsWorkspaceOnceErr
}

// setupBlockWorkspaceMount mounts the block workspace device and docker-data.
// It fails hard if any device is not available — there is no legitimate case
// where a block-mode workspace should boot without its volumes.
func setupBlockWorkspaceMount() error {
	if err := workspaceMkdirAll(workspaceMountPoint, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", workspaceMountPoint, err)
	}

	available, err := waitForWorkspaceDevice()
	if err != nil {
		return err
	}
	if !available {
		return fmt.Errorf("workspace device %s not available after %s: volume missing or host provisioning failed",
			workspaceDevicePath, time.Duration(workspaceDeviceAttempts)*workspaceDeviceInterval)
	}

	if err := workspaceMountFunc(workspaceDevicePath, workspaceMountPoint, "ext4", 0, ""); err != nil {
		if errors.Is(err, unix.EBUSY) {
			mounted, mErr := workspaceMountIsActive(workspaceDevicePath)
			if mErr != nil {
				return fmt.Errorf("verify workspace mount after EBUSY: %w", mErr)
			}
			if mounted {
				goto mountDockerData
			}
			if err := workspaceUnmountNonWorkspaceMounts(); err != nil {
				return fmt.Errorf("clear conflicting workspace mounts after EBUSY: %w", err)
			}
			if retryErr := workspaceMountFunc(workspaceDevicePath, workspaceMountPoint, "ext4", 0, ""); retryErr == nil {
				goto mountDockerData
			} else if !errors.Is(retryErr, unix.EBUSY) {
				return fmt.Errorf("retry mount %s at %s after clearing conflicts: %w", workspaceDevicePath, workspaceMountPoint, retryErr)
			}
			mounted, mErr = workspaceMountIsActive(workspaceDevicePath)
			if mErr != nil {
				return fmt.Errorf("verify workspace mount after retry EBUSY: %w", mErr)
			}
			if mounted {
				goto mountDockerData
			}
			return fmt.Errorf("mount %s at %s returned EBUSY but workspace mount is not active", workspaceDevicePath, workspaceMountPoint)
		}
		return fmt.Errorf("mount %s at %s: %w", workspaceDevicePath, workspaceMountPoint, err)
	}

mountDockerData:
	// In hybrid mode the workspace is a block device (vdb), but docker-data
	// (vdc) must also be mounted at /var/lib/docker so that Docker writes to
	// its dedicated image rather than the rootfs.  The virtiofs path already
	// handles this in setupVirtiofsWorkspace; replicate it here.
	dockerDev := dockerDevPath()
	if err := workspaceMkdirAll("/var/lib/docker", 0o755); err != nil {
		return fmt.Errorf("mkdir /var/lib/docker: %w", err)
	}
	// Wait up to 30 s for docker-data block device to appear.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, statErr := workspaceStat(dockerDev); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("docker-data device %s not available after 30s", dockerDev)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := workspaceMountFunc(dockerDev, "/var/lib/docker", "ext4", 0, ""); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount docker-data %s → /var/lib/docker: %w", dockerDev, err)
		}
	} else {
		emitDiagnostic("agent docker-data mounted at /var/lib/docker (hybrid mode)")
	}

	return nil
}

func workspaceMountIsActive(devicePath string) (bool, error) {
	raw, err := workspaceReadProcMounts("/proc/mounts")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == workspaceMountPoint {
			return fields[0] == devicePath, nil
		}
	}
	return false, nil
}

func workspaceUnmountNonWorkspaceMounts() error {
	raw, err := workspaceReadProcMounts("/proc/mounts")
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mountPoint := fields[1]
		if mountPoint == workspaceMountPoint || !strings.HasPrefix(mountPoint, workspaceMountPoint+"/") {
			continue
		}
		if err := workspaceUnmountFunc(mountPoint, 0); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}

func waitForWorkspaceDevice() (bool, error) {
	attempts := workspaceDeviceAttempts
	if attempts <= 0 {
		attempts = 1
	}

	interval := workspaceDeviceInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if _, err := workspaceStat(workspaceDevicePath); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("stat %s: %w", workspaceDevicePath, err)
		}

		if attempt < attempts-1 {
			time.Sleep(interval)
		}
	}

	return false, nil
}

func mountKernelFilesystems() {
	_ = kernelMkdirAll("/proc", 0o755)
	_ = kernelMkdirAll("/sys", 0o755)
	_ = kernelMkdirAll("/dev", 0o755)
	_ = kernelMkdirAll("/run", 0o755)
	_ = kernelMountFunc("proc", "/proc", "proc", 0, "")
	_ = kernelMountFunc("sysfs", "/sys", "sysfs", 0, "")
	_ = kernelMountFunc("devtmpfs", "/dev", "devtmpfs", 0, "")
	// In virtiofs root mode, /run inherits host uid/gid/mode semantics that
	// break daemons expecting a root-owned runtime dir (sshd, containerd shim).
	// Use a tmpfs runtime dir to restore standard Linux behavior.
	_ = kernelMountFunc("tmpfs", "/run", "tmpfs", 0, "mode=755,nosuid,nodev")
	// Mount tmpfs at /tmp with sticky bit so all users (including apt's _apt
	// sandbox user) can create temp files.  Without this, apt-key fails trying
	// to write to /tmp which is only 0755 in the base squashfs.
	_ = kernelMkdirAll("/tmp", 0o1777)
	_ = kernelMountFunc("tmpfs", "/tmp", "tmpfs", 0, "mode=1777")
	mountCgroupFilesystems()
}

func setupPTYDevices() error {
	if err := os.MkdirAll("/dev/pts", 0o755); err != nil {
		return fmt.Errorf("mkdir /dev/pts: %w", err)
	}
	if err := kernelMountFunc("devpts", "/dev/pts", "devpts", 0, "ptmxmode=0666,mode=0620"); err != nil && !errors.Is(err, unix.EBUSY) {
		return fmt.Errorf("mount devpts: %w", err)
	}
	if _, err := os.Lstat("/dev/ptmx"); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat /dev/ptmx: %w", err)
		}
		if linkErr := os.Symlink("pts/ptmx", "/dev/ptmx"); linkErr != nil && !os.IsExist(linkErr) {
			return fmt.Errorf("symlink /dev/ptmx -> pts/ptmx: %w", linkErr)
		}
	}
	return nil
}

func mountCgroupFilesystems() {
	_ = kernelMkdirAll("/sys/fs/cgroup", 0o755)
	if err := kernelMountFunc("none", "/sys/fs/cgroup", "cgroup2", 0, ""); err == nil || errors.Is(err, unix.EBUSY) {
		return
	}

	if err := kernelMountFunc("tmpfs", "/sys/fs/cgroup", "tmpfs", 0, "mode=755"); err != nil && !errors.Is(err, unix.EBUSY) {
		return
	}

	controllers := []string{"cpuset", "cpu", "cpuacct", "blkio", "memory", "devices", "freezer", "net_cls", "perf_event", "net_prio", "hugetlb", "pids"}
	for _, controller := range controllers {
		path := "/sys/fs/cgroup/" + controller
		_ = kernelMkdirAll(path, 0o755)
		err := kernelMountFunc("cgroup", path, "cgroup", 0, controller)
		if err != nil && !errors.Is(err, unix.EBUSY) {
			continue
		}
	}
}
