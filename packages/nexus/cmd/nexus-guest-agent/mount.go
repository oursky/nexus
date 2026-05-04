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
	workspaceBaseDevicePath         = "/dev/vde"
	workspaceDeviceAttempts         = 300
	workspaceDeviceInterval         = 100 * time.Millisecond
	workspaceMkdirAll               = os.MkdirAll
	workspaceStat                   = os.Stat
	workspaceMountFunc              = unix.Mount
	workspaceUnmountFunc            = unix.Unmount
	workspaceReadProcMounts         = os.ReadFile
	kernelMkdirAll                  = os.MkdirAll
	kernelMountFunc                 = unix.Mount

	// Hybrid overlayfs mount points.
	workspaceLowerMountPoint = "/workspace-lower"
	workspaceUpperMountPoint = "/workspace-upper"
	workspaceOverlayWorkDir  = "/workspace-upper/.workdir"
	workspaceBaseMountPoint  = "/workspace-base"

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

// setupBlockWorkspaceMount assembles the hybrid overlayfs workspace.
// It mounts /dev/vdb as the mutable upperdir, /dev/vde as the read-only
// baked base lowerdir, and virtiofs as the live host project lowerdir.
// If overlay assembly fails, it falls back to mounting the base image
// directly at /workspace.
func setupBlockWorkspaceMount() error {
	for _, dir := range []string{workspaceMountPoint, workspaceLowerMountPoint, workspaceUpperMountPoint, workspaceBaseMountPoint} {
		if err := workspaceMkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	available, err := waitForWorkspaceDevice()
	if err != nil {
		return err
	}
	if !available {
		return fmt.Errorf("workspace device %s not available after %s: volume missing or host provisioning failed",
			workspaceDevicePath, time.Duration(workspaceDeviceAttempts)*workspaceDeviceInterval)
	}

	if err := tryMountHybridOverlay(); err != nil {
		emitDiagnostic("agent hybrid overlay failed (%v), falling back to base image", err)
		if fallbackErr := mountBaseWorkspaceDirect(); fallbackErr != nil {
			return fallbackErr
		}
	}

	return mountDockerData()
}

// tryMountHybridOverlay assembles the overlayfs workspace:
//   lowerdir: virtiofs "nexus-workspace" : /workspace-base (baked base)
//   upperdir: /dev/vdb mounted at /workspace-upper (mutable workspace state)
//   workdir:  /workspace-upper/.workdir
//   merged:   /workspace
func tryMountHybridOverlay() error {
	// Mount block device as the overlay upperdir.
	if err := workspaceMountFunc(workspaceDevicePath, workspaceUpperMountPoint, "ext4", 0, ""); err != nil {
		if errors.Is(err, unix.EBUSY) {
			if !mountPointIsActive(workspaceDevicePath, workspaceUpperMountPoint) {
				return fmt.Errorf("mount %s at %s returned EBUSY but not active", workspaceDevicePath, workspaceUpperMountPoint)
			}
		} else {
			return fmt.Errorf("mount %s at %s: %w", workspaceDevicePath, workspaceUpperMountPoint, err)
		}
	}

	// Ensure overlay workdir exists on the same filesystem as upperdir.
	if err := workspaceMkdirAll(workspaceOverlayWorkDir, 0o755); err != nil {
		_ = workspaceUnmountFunc(workspaceUpperMountPoint, 0)
		return fmt.Errorf("mkdir %s: %w", workspaceOverlayWorkDir, err)
	}

	// Mount baked base lowerdir (read-only project snapshot).
	if err := workspaceMountFunc(workspaceBaseDevicePath, workspaceBaseMountPoint, "ext4", unix.MS_RDONLY, ""); err != nil {
		if errors.Is(err, unix.EBUSY) {
			if !mountPointIsActive(workspaceBaseDevicePath, workspaceBaseMountPoint) {
				_ = workspaceUnmountFunc(workspaceUpperMountPoint, 0)
				return fmt.Errorf("mount base %s at %s returned EBUSY but not active", workspaceBaseDevicePath, workspaceBaseMountPoint)
			}
		} else {
			_ = workspaceUnmountFunc(workspaceUpperMountPoint, 0)
			return fmt.Errorf("mount workspace base %s → %s: %w", workspaceBaseDevicePath, workspaceBaseMountPoint, err)
		}
	}

	// Mount virtiofs lowerdir (read-only host project directory).
	if err := workspaceMountFunc("nexus-workspace", workspaceLowerMountPoint, "virtiofs", unix.MS_RDONLY, ""); err != nil {
		_ = workspaceUnmountFunc(workspaceBaseMountPoint, 0)
		_ = workspaceUnmountFunc(workspaceUpperMountPoint, 0)
		if errors.Is(err, unix.EBUSY) {
			if !mountPointIsActive("nexus-workspace", workspaceLowerMountPoint) {
				return fmt.Errorf("mount virtiofs at %s returned EBUSY but not active", workspaceLowerMountPoint)
			}
		} else {
			return fmt.Errorf("mount virtiofs nexus-workspace → %s: %w", workspaceLowerMountPoint, err)
		}
	}

	// Assemble overlayfs at /workspace.
	lowerdir := workspaceLowerMountPoint + ":" + workspaceBaseMountPoint
	overlayOpts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerdir, workspaceUpperMountPoint, workspaceOverlayWorkDir)
	if err := workspaceMountFunc("overlay", workspaceMountPoint, "overlay", 0, overlayOpts); err != nil {
		_ = workspaceUnmountFunc(workspaceLowerMountPoint, 0)
		_ = workspaceUnmountFunc(workspaceBaseMountPoint, 0)
		_ = workspaceUnmountFunc(workspaceUpperMountPoint, 0)
		if errors.Is(err, unix.EBUSY) {
			if !mountPointIsActive("overlay", workspaceMountPoint) {
				return fmt.Errorf("mount overlay at %s returned EBUSY but not active", workspaceMountPoint)
			}
		} else {
			return fmt.Errorf("mount overlay at %s: %w", workspaceMountPoint, err)
		}
	}

	emitDiagnostic("agent hybrid overlay workspace mounted at %s (lowerdirs=%s)", workspaceMountPoint, lowerdir)
	return nil
}

// mountBaseWorkspaceDirect mounts the baked base image directly at /workspace
// when overlayfs assembly fails. This ensures the workspace is still usable
// even if overlay cannot be mounted.
func mountBaseWorkspaceDirect() error {
	if err := workspaceMountFunc(workspaceBaseDevicePath, workspaceMountPoint, "ext4", 0, ""); err != nil {
		if errors.Is(err, unix.EBUSY) {
			if mountPointIsActive(workspaceBaseDevicePath, workspaceMountPoint) {
				emitDiagnostic("agent workspace mounted from base image %s (fallback)", workspaceBaseDevicePath)
				return nil
			}
		}
		return fmt.Errorf("mount fallback base %s at %s: %w", workspaceBaseDevicePath, workspaceMountPoint, err)
	}
	emitDiagnostic("agent workspace mounted from base image %s (fallback)", workspaceBaseDevicePath)
	return nil
}

// mountDockerData waits for the docker-data block device and mounts it at
// /var/lib/docker.
func mountDockerData() error {
	dockerDev := dockerDevPath()
	if err := workspaceMkdirAll("/var/lib/docker", 0o755); err != nil {
		return fmt.Errorf("mkdir /var/lib/docker: %w", err)
	}
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

// mountPointIsActive reports whether source is mounted at target according to
// /proc/mounts.
func mountPointIsActive(source, target string) bool {
	raw, err := workspaceReadProcMounts("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == target {
			return fields[0] == source
		}
	}
	return false
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
