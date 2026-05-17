//go:build linux

package hostsetup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// DetectPackageManager returns the name of the system package manager
// available on PATH. Returns "" if no known package manager is found.
func DetectPackageManager() string {
	for _, pm := range []string{"apt-get", "dnf", "yum", "zypper", "pacman", "apk", "xbps-install"} {
		if _, err := exec.LookPath(pm); err == nil {
			return pm
		}
	}
	return ""
}

func isRoot() bool {
	return os.Geteuid() == 0
}

func sudoPrefix() []string {
	if isRoot() {
		return nil
	}
	return []string{"sudo"}
}

// EnsureLinuxRootfsTools installs e2fsprogs and coreutils via the
// detected package manager. Verifies e2fsck, resize2fs, and truncate
// are on PATH after installation.
func EnsureLinuxRootfsTools() error {
	if toolsPresent() {
		return nil
	}

	pm := DetectPackageManager()
	if pm == "" {
		return fmt.Errorf("hostsetup: no supported package manager found — install e2fsprogs and coreutils manually")
	}

	fmt.Fprintf(os.Stderr, "hostsetup: installing e2fsprogs + coreutils via %s ...\n", pm)

	var installArgs [][]string
	switch pm {
	case "apt-get":
		installArgs = [][]string{
			{"apt-get", "update", "-qq"},
			{"apt-get", "install", "-y", "-qq", "e2fsprogs", "coreutils"},
		}
		os.Setenv("DEBIAN_FRONTEND", "noninteractive")
	case "dnf":
		installArgs = [][]string{{"dnf", "install", "-y", "e2fsprogs", "coreutils"}}
	case "yum":
		installArgs = [][]string{{"yum", "install", "-y", "e2fsprogs", "coreutils"}}
	case "zypper":
		installArgs = [][]string{{"zypper", "--non-interactive", "install", "-y", "e2fsprogs", "coreutils"}}
	case "pacman":
		installArgs = [][]string{{"pacman", "-Sy", "--needed", "--noconfirm", "e2fsprogs", "coreutils"}}
	case "apk":
		installArgs = [][]string{{"apk", "add", "--no-cache", "e2fsprogs", "coreutils"}}
	case "xbps-install":
		installArgs = [][]string{{"xbps-install", "-Sy", "e2fsprogs", "coreutils"}}
	}

	for _, args := range installArgs {
		cmdArgs := append(sudoPrefix(), args...)
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		if pm == "apt-get" {
			cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		}
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("hostsetup: %s install failed: %w", pm, err)
		}
	}

	if !toolsPresent() {
		return fmt.Errorf("hostsetup: e2fsprogs/coreutils still missing after install")
	}
	return nil
}

// toolsPresent checks whether e2fsck, resize2fs, and truncate are on PATH.
func toolsPresent() bool {
	for _, tool := range []string{"e2fsck", "resize2fs", "truncate"} {
		if _, err := exec.LookPath(tool); err != nil {
			return false
		}
	}
	return true
}

// EnsureLinuxZstd installs zstd via the detected package manager.
func EnsureLinuxZstd() error {
	if _, err := exec.LookPath("zstd"); err == nil {
		return nil
	}

	pm := DetectPackageManager()
	if pm == "" {
		return fmt.Errorf("hostsetup: no supported package manager found — install zstd manually")
	}

	fmt.Fprintf(os.Stderr, "hostsetup: installing zstd via %s ...\n", pm)
	var args []string
	switch pm {
	case "apt-get":
		args = append(sudoPrefix(), "apt-get", "install", "-y", "-qq", "zstd")
	case "dnf":
		args = append(sudoPrefix(), "dnf", "install", "-y", "zstd")
	case "yum":
		args = append(sudoPrefix(), "yum", "install", "-y", "zstd")
	case "zypper":
		args = append(sudoPrefix(), "zypper", "--non-interactive", "install", "-y", "zstd")
	case "pacman":
		args = append(sudoPrefix(), "pacman", "-Sy", "--needed", "--noconfirm", "zstd")
	case "apk":
		args = append(sudoPrefix(), "apk", "add", "--no-cache", "zstd")
	case "xbps-install":
		args = append(sudoPrefix(), "xbps-install", "-Sy", "zstd")
	default:
		return fmt.Errorf("hostsetup: unsupported package manager: %s", pm)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// EnsureLinuxXFSProgs installs xfsprogs (for mkfs.xfs) via the detected
// package manager.
func EnsureLinuxXFSProgs() error {
	if _, err := exec.LookPath("mkfs.xfs"); err == nil {
		return nil
	}

	pm := DetectPackageManager()
	if pm == "" {
		return fmt.Errorf("hostsetup: no supported package manager found — install xfsprogs manually")
	}

	fmt.Fprintf(os.Stderr, "hostsetup: installing xfsprogs via %s ...\n", pm)
	var args []string
	switch pm {
	case "apt-get":
		args = append(sudoPrefix(), "apt-get", "install", "-y", "-qq", "xfsprogs")
	case "dnf":
		args = append(sudoPrefix(), "dnf", "install", "-y", "xfsprogs")
	case "yum":
		args = append(sudoPrefix(), "yum", "install", "-y", "xfsprogs")
	case "zypper":
		args = append(sudoPrefix(), "zypper", "--non-interactive", "install", "-y", "xfsprogs")
	case "pacman":
		args = append(sudoPrefix(), "pacman", "-Sy", "--needed", "--noconfirm", "xfsprogs")
	case "apk":
		args = append(sudoPrefix(), "apk", "add", "--no-cache", "xfsprogs")
	case "xbps-install":
		args = append(sudoPrefix(), "xbps-install", "-Sy", "xfsprogs")
	default:
		return fmt.Errorf("hostsetup: unsupported package manager: %s", pm)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// EnsureLinuxHostPrereqs performs best-effort host configuration for
// libkrun VMs: KVM device access, user namespaces, AppArmor relaxation.
// Returns nil even when some checks can't be auto-fixed (warnings only).
func EnsureLinuxHostPrereqs() error {
	// /dev/kvm accessibility
	if _, err := os.Stat("/dev/kvm"); err == nil {
		// Check if it's readable
		f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err != nil {
			// Try adding user to kvm group
			if _, gerr := exec.LookPath("getent"); gerr == nil {
				cmd := exec.Command("getent", "group", "kvm")
				if err := cmd.Run(); err == nil {
					userCmd := exec.Command("id", "-un")
					userOut, _ := userCmd.Output()
					user := strings.TrimSpace(string(userOut))
					fmt.Fprintf(os.Stderr, "hostsetup: adding user %s to group kvm for /dev/kvm access\n", user)
					addCmd := exec.Command("sudo", "usermod", "-aG", "kvm", user)
					addCmd.Stdout = os.Stderr
					addCmd.Stderr = os.Stderr
					if err := addCmd.Run(); err != nil {
						fmt.Fprintf(os.Stderr, "hostsetup: warning: could not add user to kvm group: %v\n", err)
					} else {
						fmt.Fprintf(os.Stderr, "hostsetup: added to kvm group — log out and back in for it to take effect\n")
					}
				}
			}
		} else {
			f.Close()
		}
	} else {
		fmt.Fprintf(os.Stderr, "hostsetup: warning: /dev/kvm not found — enable CPU virtualization in firmware\n")
	}

	didSysctl := false

	// Enable unprivileged user namespaces
	if v, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone"); err == nil {
		if strings.TrimSpace(string(v)) == "0" {
			fmt.Fprintf(os.Stderr, "hostsetup: enabling kernel.unprivileged_userns_clone=1\n")
			cmd := exec.Command("sudo", "sysctl", "-w", "kernel.unprivileged_userns_clone=1")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "hostsetup: warning: could not set unprivileged_userns_clone: %v\n", err)
			} else {
				didSysctl = true
			}
		}
	}

	// Relax AppArmor userns restriction
	if v, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); err == nil {
		if strings.TrimSpace(string(v)) == "1" {
			fmt.Fprintf(os.Stderr, "hostsetup: setting kernel.apparmor_restrict_unprivileged_userns=0\n")
			cmd := exec.Command("sudo", "sysctl", "-w", "kernel.apparmor_restrict_unprivileged_userns=0")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "hostsetup: warning: could not set apparmor_restrict_unprivileged_userns: %v\n", err)
			} else {
				didSysctl = true
			}
		}
	}

	// Persist sysctl settings
	if didSysctl || !fileExists("/etc/sysctl.d/99-nexus-libkrun.conf") {
		var lines []string
		if v, _ := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone"); strings.TrimSpace(string(v)) == "1" {
			lines = append(lines, "kernel.unprivileged_userns_clone=1")
		}
		if v, _ := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); strings.TrimSpace(string(v)) == "0" {
			lines = append(lines, "kernel.apparmor_restrict_unprivileged_userns=0")
		}
		if len(lines) > 0 {
			content := strings.Join(lines, "\n") + "\n"
			cmd := exec.Command("sudo", "tee", "/etc/sysctl.d/99-nexus-libkrun.conf")
			cmd.Stdin = strings.NewReader(content)
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "hostsetup: warning: could not persist sysctl settings: %v\n", err)
			}
		}
	}

	// Check max_user_namespaces
	if v, err := os.ReadFile("/proc/sys/user/max_user_namespaces"); err == nil {
		trimmed := strings.TrimSpace(string(v))
		if n, err := strconv.Atoi(trimmed); err == nil && n == 0 {
			fmt.Fprintf(os.Stderr, "hostsetup: warning: max_user_namespaces is 0 — passt cannot create namespaces\n")
		}
	}

	return nil
}

// EnsureLinuxDaemonDataDir creates /data/nexus and /data/nexus/default
// with the correct ownership for libkrun workspace storage.
func EnsureLinuxDaemonDataDir() error {
	uid := os.Getuid()
	gid := os.Getgid()

	dirs := []string{"/data/nexus", "/data/nexus/default"}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			cmd := exec.Command("sudo", "mkdir", "-p", d)
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("hostsetup: mkdir %s: %w", d, err)
			}
		}
	}

	// Ensure ownership
	for _, d := range dirs {
		info, err := os.Stat(d)
		if err != nil {
			continue
		}
		if stat, ok := info.Sys().(interface {
			Uid() int
			Gid() int
		}); ok {
			if stat.Uid() != uid || stat.Gid() != gid {
				cmd := exec.Command("sudo", "chown", fmt.Sprintf("%d:%d", uid, gid), d)
				cmd.Stdout = os.Stderr
				cmd.Stderr = os.Stderr
				_ = cmd.Run()
			}
		}
	}

	fmt.Fprintf(os.Stderr, "hostsetup: ensured /data/nexus/default (libkrun workspace store)\n")
	return nil
}

// EnsureLinuxReflinkVolume checks whether /data/nexus supports reflink
// copies. If not, creates a sparse XFS loop image at
// /var/lib/nexus-xfs-backing.img and mounts it.
func EnsureLinuxReflinkVolume() error {
	mountPoint := "/data/nexus"
	backingFile := "/var/lib/nexus-xfs-backing.img"
	loopTarget := mountPoint

	// Check if reflink is already supported at mountPoint
	if supportsReflink(mountPoint) {
		_ = os.MkdirAll(filepath.Join(mountPoint, "default"), 0o755)
		fmt.Fprintf(os.Stderr, "hostsetup: %s supports reflink\n", mountPoint)
		return nil
	}

	// Check if reflink is supported at mountPoint/default
	defaultDir := filepath.Join(mountPoint, "default")
	if fileExists(defaultDir) && supportsReflink(defaultDir) {
		fmt.Fprintf(os.Stderr, "hostsetup: %s supports reflink\n", defaultDir)
		return nil
	}

	// Check if mountPoint has content; if so, mount at default/ instead
	entries, _ := os.ReadDir(mountPoint)
	if len(entries) > 0 {
		loopTarget = defaultDir
	}
	if err := os.MkdirAll(loopTarget, 0o755); err != nil {
		cmd := exec.Command("sudo", "mkdir", "-p", loopTarget)
		cmd.Run()
	}

	sizeGB := os.Getenv("NEXUS_XFS_SIZE_GB")
	if sizeGB == "" {
		sizeGB = "20"
	}

	if !fileExists(backingFile) {
		fmt.Fprintf(os.Stderr, "hostsetup: creating XFS loop volume (%s GB at %s)\n", sizeGB, backingFile)
		sudoMkdir := exec.Command("sudo", "mkdir", "-p", filepath.Dir(backingFile))
		sudoMkdir.Run()

		truncate := exec.Command("sudo", "truncate", "-s", sizeGB+"G", backingFile)
		truncate.Stdout = os.Stderr
		truncate.Stderr = os.Stderr
		if err := truncate.Run(); err != nil {
			return fmt.Errorf("hostsetup: truncate %s: %w", backingFile, err)
		}

		mkfs := exec.Command("sudo", "mkfs.xfs", "-f", "-m", "reflink=1", backingFile)
		mkfs.Stdout = os.Stderr
		mkfs.Stderr = os.Stderr
		if err := mkfs.Run(); err != nil {
			return fmt.Errorf("hostsetup: mkfs.xfs %s: %w", backingFile, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "hostsetup: reusing existing XFS backing file %s\n", backingFile)
	}

	mount := exec.Command("sudo", "mount", "-o", "loop", backingFile, loopTarget)
	mount.Stdout = os.Stderr
	mount.Stderr = os.Stderr
	if err := mount.Run(); err != nil {
		return fmt.Errorf("hostsetup: mount %s at %s: %w", backingFile, loopTarget, err)
	}

	fmt.Fprintf(os.Stderr, "hostsetup: mounted XFS with reflink=1 at %s\n", loopTarget)

	// Ensure default/ exists under mountPoint
	_ = os.MkdirAll(defaultDir, 0o755)
	return nil
}

// supportsReflink tests whether path supports reflink copies by attempting
// a cp --reflink=always between two temp files.
func supportsReflink(dir string) bool {
	src := filepath.Join(dir, ".nexus-reflink-probe-src")
	dst := filepath.Join(dir, ".nexus-reflink-probe-dst")

	f, err := os.Create(src)
	if err != nil {
		return false
	}
	f.Close()
	defer os.Remove(src)

	cmd := exec.Command("cp", "--reflink=always", src, dst)
	if err := cmd.Run(); err != nil {
		return false
	}
	os.Remove(dst)
	return true
}

// EnsurePasst installs the passt binary to ~/.local/bin/passt if it
// does not already exist.
func EnsurePasst() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("hostsetup: home dir: %w", err)
	}
	dest := filepath.Join(home, ".local", "bin", "passt")

	if fi, err := os.Stat(dest); err == nil && fi.Mode()&0o111 != 0 {
		return nil // already installed
	}

	// Check system passt
	if sysPasst, err := exec.LookPath("passt"); err == nil {
		fmt.Fprintf(os.Stderr, "hostsetup: copying system passt from %s to %s\n", sysPasst, dest)
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
		data, err := os.ReadFile(sysPasst)
		if err != nil {
			return fmt.Errorf("hostsetup: read system passt: %w", err)
		}
		if err := os.WriteFile(dest, data, 0o755); err != nil {
			return fmt.Errorf("hostsetup: write passt to %s: %w", dest, err)
		}
		return nil
	}

	// Download static passt for amd64
	fmt.Fprintf(os.Stderr, "hostsetup: downloading passt static binary ...\n")
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)

	cmd := exec.Command("curl", "-fsSL", "--retry", "3",
		"https://passt.top/builds/latest/x86_64/passt", "-o", dest)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hostsetup: download passt: %w", err)
	}

	if err := os.Chmod(dest, 0o755); err != nil {
		return fmt.Errorf("hostsetup: chmod passt: %w", err)
	}
	fmt.Fprintf(os.Stderr, "hostsetup: passt installed at %s\n", dest)
	return nil
}

// EnsureSmolVMLibs downloads and extracts the smolvm libkrun tarball
// into ~/.local/share/nexus/lib/.
func EnsureSmolVMLibs(version string) error {
	if version == "" {
		version = "v0.5.19"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	libDir := filepath.Join(home, ".local", "share", "nexus", "lib")

	// Check if libkrun already exists
	if _, err := os.Stat(filepath.Join(libDir, "libkrun.so.1")); err == nil {
		fmt.Fprintf(os.Stderr, "hostsetup: libkrun already present at %s\n", libDir)
		return nil
	}

	verPlain := strings.TrimPrefix(version, "v")
	tarball := fmt.Sprintf("smolvm-%s-linux-x86_64.tar.gz", verPlain)
	baseURL := fmt.Sprintf("https://github.com/smol-machines/smolvm/releases/download/%s", version)

	tmpDir, err := os.MkdirTemp("", "nexus-smolvm-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	tarballPath := filepath.Join(tmpDir, tarball)
	checksumsPath := filepath.Join(tmpDir, "checksums.sha256")

	fmt.Fprintf(os.Stderr, "hostsetup: downloading smolvm checksums (%s) ...\n", version)
	curlChecksum := exec.Command("curl", "-fsSL", "-o", checksumsPath,
		baseURL+"/checksums.sha256")
	curlChecksum.Stdout = os.Stderr
	curlChecksum.Stderr = os.Stderr
	if err := curlChecksum.Run(); err != nil {
		return fmt.Errorf("hostsetup: download smolvm checksums: %w", err)
	}

	fmt.Fprintf(os.Stderr, "hostsetup: downloading %s ...\n", tarball)
	curlTarball := exec.Command("curl", "-fsSL", "-o", tarballPath,
		baseURL+"/"+tarball)
	curlTarball.Stdout = os.Stderr
	curlTarball.Stderr = os.Stderr
	if err := curlTarball.Run(); err != nil {
		return fmt.Errorf("hostsetup: download smolvm tarball: %w", err)
	}

	fmt.Fprintf(os.Stderr, "hostsetup: verifying tarball checksum ...\n")
	verifyCmd := exec.Command("sh", "-c",
		fmt.Sprintf("cd %s && grep '%s' checksums.sha256 | sha256sum -c -", tmpDir, tarball))
	verifyCmd.Stdout = os.Stderr
	verifyCmd.Stderr = os.Stderr
	if err := verifyCmd.Run(); err != nil {
		return fmt.Errorf("hostsetup: smolvm checksum verification failed: %w", err)
	}

	extractDir := filepath.Join(tmpDir, "extract")
	_ = os.MkdirAll(extractDir, 0o755)

	fmt.Fprintf(os.Stderr, "hostsetup: extracting libkrun libraries ...\n")
	tarCmd := exec.Command("tar", "-xzf", tarballPath, "-C", extractDir,
		"--strip-components=2", fmt.Sprintf("smolvm-%s-linux-x86_64/lib", verPlain))
	tarCmd.Stdout = os.Stderr
	tarCmd.Stderr = os.Stderr
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("hostsetup: extract libkrun: %w", err)
	}

	_ = os.MkdirAll(libDir, 0o755)
	cpCmd := exec.Command("cp", "-a", extractDir+"/.", libDir+"/")
	cpCmd.Stdout = os.Stderr
	cpCmd.Stderr = os.Stderr
	if err := cpCmd.Run(); err != nil {
		return fmt.Errorf("hostsetup: copy libkrun to %s: %w", libDir, err)
	}

	fmt.Fprintf(os.Stderr, "hostsetup: installed libkrun + libkrunfw (%s) → %s\n", version, libDir)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
