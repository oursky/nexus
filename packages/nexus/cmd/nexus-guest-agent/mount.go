//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

	// virtiofsWorkspaceOnce ensures setupVirtiofsWorkspace runs exactly once.
	// Unlike ext4 block devices (which return EBUSY on re-mount), overlayfs
	// silently stacks a new mount on each call, so an idempotency check via
	// the mount return code is not reliable.
	virtiofsWorkspaceOnce    sync.Once
	virtiofsWorkspaceOnceErr error
)

// isVirtiofsWorkspaceMode reports whether the workspace is virtiofs+overlayfs.
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

// overlayDevPath returns the block device for the workspace overlay upper layer.
// Default /dev/vdb (virtiofs mode); overridden to /dev/vda in
// libkrun container mode via NEXUS_OVERLAY_DEV.
func overlayDevPath() string {
	if v := os.Getenv("NEXUS_OVERLAY_DEV"); v != "" {
		return v
	}
	return "/dev/vdb"
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

// setupVirtiofsWorkspace assembles the virtiofs+overlayfs workspace mount.
//
// Device layout is resolved from environment variables set by the host daemon:
//
//	virtiofs "nexus-workspace"   project dir (ro lower layer)
//	NEXUS_OVERLAY_DEV (/dev/vdb) workspace-overlay.ext4 (rw upper layer)
//	NEXUS_DOCKER_DEV  (/dev/vdc) docker-data.ext4 → /var/lib/docker
//	NEXUS_CONFIG_DEV  (/dev/vdd) hostconfig.ext4  → /run/nexus-host  (read-only)
//
// virtiofs mode falls back to /dev/vdb, /dev/vdc, /dev/vdd.
func setupVirtiofsWorkspace() error {
	overlayDev := overlayDevPath()
	dockerDev := dockerDevPath()

	lowerDir := "/mnt/nexus-lower"
	overlayMnt := "/mnt/nexus-overlay"

	for _, dir := range []string{lowerDir, overlayMnt, workspaceMountPoint, "/var/lib/docker"} {
		if err := workspaceMkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Mount virtiofs tag → lower dir (read-only project directory passthrough).
	if err := workspaceMountFunc("nexus-workspace", lowerDir, "virtiofs", unix.MS_RDONLY, ""); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount virtiofs nexus-workspace → %s: %w", lowerDir, err)
		}
	} else {
		emitDiagnostic("agent virtiofs lower mounted at %s", lowerDir)
	}

	// Wait for workspace-overlay ext4 device and mount it.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := workspaceStat(overlayDev); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("workspace overlay device %s not available after 30s", overlayDev)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := workspaceMountFunc(overlayDev, overlayMnt, "ext4", 0, ""); err != nil && !errors.Is(err, unix.EBUSY) {
		return fmt.Errorf("mount overlay ext4 %s → %s: %w", overlayDev, overlayMnt, err)
	}

	// Ensure upper and work directories exist inside the overlay ext4.
	upperDir := overlayMnt + "/upper"
	workDir := overlayMnt + "/work"
	for _, dir := range []string{upperDir, workDir} {
		if err := workspaceMkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir overlayfs dir %s: %w", dir, err)
		}
	}

	// Pre-seed .git into the overlay upper layer.
	// overlayfs triggers copy-up when any file in the lower layer is first
	// modified.  Copy-up calls FS_IOC_GETFLAGS on the lower file to preserve
	// filesystem flags, but virtiofs does not implement this ioctl (EOPNOTSUPP /
	// errno -95).  The kernel logs "overlayfs: failed to retrieve lower fileattr"
	// and all git write operations fail with EPERM/ENOTSUP.
	// userxattr alone does not suppress the fileattr copy-up path.
	// The fix: eagerly copy .git from virtiofs lower → ext4 upper before
	// mounting overlayfs, so the directory is already in the upper layer and
	// copy-up is never attempted for it.
	if err := preseedUpperDir(lowerDir, upperDir, ".git"); err != nil {
		emitDiagnostic("agent preseed .git warning: %v", err)
		// Non-fatal: workspace mounts regardless; git ops may still fail.
	}

	// Mount overlayfs: lower=virtiofs, upper=overlay ext4, merged → /workspace.
	// userxattr: avoids FS_IOC_GETFLAGS ioctls on the lower layer which virtiofs
	// does not support (EOPNOTSUPP), preventing copy-up failures for .git dirs.
	overlayOpts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,userxattr", lowerDir, upperDir, workDir)
	if err := workspaceMountFunc("overlay", workspaceMountPoint, "overlay", 0, overlayOpts); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount overlayfs → %s: %w", workspaceMountPoint, err)
		}
	} else {
		emitDiagnostic("agent overlayfs workspace mounted at %s", workspaceMountPoint)
	}

	// Wait for docker-data ext4 device and mount to /var/lib/docker.
	deadline = time.Now().Add(30 * time.Second)
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
	return setupWorkspaceMountWithRequirement(false)
}

func setupWorkspaceMountRequired() error {
	if isVirtiofsWorkspaceMode() {
		return setupVirtiofsWorkspaceOnce()
	}
	return setupWorkspaceMountWithRequirement(true)
}

// setupVirtiofsWorkspaceOnce calls setupVirtiofsWorkspace exactly once.
// Overlayfs does not return EBUSY on re-mount — it silently stacks a new
// mount — so idempotency must be enforced here rather than at the syscall.
func setupVirtiofsWorkspaceOnce() error {
	virtiofsWorkspaceOnce.Do(func() {
		virtiofsWorkspaceOnceErr = setupVirtiofsWorkspace()
	})
	return virtiofsWorkspaceOnceErr
}

func setupWorkspaceMountWithRequirement(required bool) error {
	if err := workspaceMkdirAll(workspaceMountPoint, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", workspaceMountPoint, err)
	}

	available, err := waitForWorkspaceDevice()
	if err != nil {
		return err
	}
	if !available {
		if required {
			return fmt.Errorf("workspace device %s not available", workspaceDevicePath)
		}
		return nil
	}

	if err := workspaceMountFunc(workspaceDevicePath, workspaceMountPoint, "ext4", 0, ""); err != nil {
		if errors.Is(err, unix.EBUSY) {
			mounted, mErr := workspaceMountIsActive(workspaceDevicePath)
			if mErr != nil {
				return fmt.Errorf("verify workspace mount after EBUSY: %w", mErr)
			}
			if mounted {
				return nil
			}
			if err := workspaceUnmountNonWorkspaceMounts(); err != nil {
				return fmt.Errorf("clear conflicting workspace mounts after EBUSY: %w", err)
			}
			if retryErr := workspaceMountFunc(workspaceDevicePath, workspaceMountPoint, "ext4", 0, ""); retryErr == nil {
				return nil
			} else if !errors.Is(retryErr, unix.EBUSY) {
				return fmt.Errorf("retry mount %s at %s after clearing conflicts: %w", workspaceDevicePath, workspaceMountPoint, retryErr)
			}
			mounted, mErr = workspaceMountIsActive(workspaceDevicePath)
			if mErr != nil {
				return fmt.Errorf("verify workspace mount after retry EBUSY: %w", mErr)
			}
			if mounted {
				return nil
			}
			return fmt.Errorf("mount %s at %s returned EBUSY but workspace mount is not active", workspaceDevicePath, workspaceMountPoint)
		}
		return fmt.Errorf("mount %s at %s: %w", workspaceDevicePath, workspaceMountPoint, err)
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

// preseedUpperDir copies srcName (e.g. ".git") from srcRoot into dstRoot so
// that overlayfs sees it already in the upper layer and never needs to perform
// a copy-up from the virtiofs lower layer.  Copy-up invokes FS_IOC_GETFLAGS
// which virtiofs does not implement (EOPNOTSUPP), making all git write
// operations fail.  Preseeding prevents that inode path entirely.
//
// If dstRoot/srcName already exists the function is a no-op (idempotent across
// VM restarts — the ext4 upper layer persists between boots).
func preseedUpperDir(srcRoot, dstRoot, srcName string) error {
	src := filepath.Join(srcRoot, srcName)
	dst := filepath.Join(dstRoot, srcName)

	if _, err := os.Lstat(src); err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to seed
		}
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if _, err := os.Lstat(dst); err == nil {
		return nil // already seeded
	}

	return copyDirRecursive(src, dst)
}

// copyDirRecursive copies the directory tree rooted at src to dst.
// Symbolic links are reproduced as symlinks; regular files are byte-copied.
// File modes are preserved on a best-effort basis.
func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		// Use Lstat so symlinks are not followed.
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}

		// Symlink
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}

		// Directory
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		// Regular file
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
