package firecracker

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// buildHostConfigDrive creates a small ext4 image at destPath populated with
// the host user's config files (gitconfig, SSH public material, tool configs).
// The image is attached to the VM as /dev/vdc and the guest agent mounts it at
// /run/nexus-host, then applies the configs to standard paths.
//
// mkfs.ext4 and debugfs are used — no root required.
func buildHostConfigDrive(home, destPath string) error {
	const sizeMiB = 32

	// Create or truncate the image file.
	if err := os.Truncate(destPath, int64(sizeMiB)*1024*1024); err != nil {
		f, err2 := os.Create(destPath)
		if err2 != nil {
			return fmt.Errorf("create config drive: %w", err2)
		}
		if err2 := f.Truncate(int64(sizeMiB) * 1024 * 1024); err2 != nil {
			f.Close()
			return fmt.Errorf("size config drive: %w", err2)
		}
		f.Close()
	}

	// Format as ext4.
	if out, err := exec.Command("mkfs.ext4", "-F", "-L", "nexus-host-config", destPath).CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Collect files to inject: src path on host → dst path inside the image.
	type entry struct{ src, dst string }
	var files []entry

	add := func(src, dst string) {
		if _, err := os.Stat(src); err == nil {
			files = append(files, entry{src, dst})
		}
	}

	// Git config.
	add(home+"/.gitconfig", ".gitconfig")

	// SSH public material (no private keys — agent forwarded via vsock).
	add(home+"/.ssh/known_hosts", ".ssh/known_hosts")
	add(home+"/.ssh/config", ".ssh/config")

	// Tool auth configs — only the specific files we know are small and useful.
	add(home+"/.config/gh/hosts.yml", ".config/gh/hosts.yml")
	add(home+"/.config/gh/config.yml", ".config/gh/config.yml")
	add(home+"/.config/opencode/opencode.json", ".config/opencode/opencode.json")
	add(home+"/.config/opencode/ocx.jsonc", ".config/opencode/ocx.jsonc")
	add(home+"/.opencode/opencode.json", ".opencode/opencode.json")
	add(home+"/.opencode/opencode.jsonc", ".opencode/opencode.jsonc")
	add(home+"/.config/claude/credentials.json", ".config/claude/credentials.json")
	add(home+"/.config/claude/settings.json", ".config/claude/settings.json")

	// Host DNS resolver config so the guest can use the same upstream DNS
	// servers (important in networks where public DNS is blocked).
	add("/etc/resolv.conf", ".resolv.conf")

	if len(files) == 0 {
		return nil
	}

	// Ensure parent directories exist inside the image, then write each file.
	dirs := make(map[string]struct{})
	for _, f := range files {
		d := filepath.Dir(f.dst)
		if d != "." {
			dirs[d] = struct{}{}
		}
	}

	run := func(args ...string) error {
		cmd := exec.Command("debugfs", append([]string{"-w", destPath, "-R"}, strings.Join(args, " "))...)
		if out, err := cmd.CombinedOutput(); err != nil {
			detail := strings.TrimSpace(string(out))
			if detail != "" && !strings.Contains(detail, "already exists") {
				return fmt.Errorf("debugfs %v: %w: %s", args, err, detail)
			}
		}
		return nil
	}

	for dir := range dirs {
		// Create each path component.
		parts := strings.Split(dir, "/")
		for i := range parts {
			_ = run("mkdir", strings.Join(parts[:i+1], "/"))
		}
	}

	for _, f := range files {
		if err := run("write", f.src, f.dst); err != nil {
			log.Printf("[firecracker] host config drive: skipping %s: %v", f.dst, err)
		}
	}

	return nil
}
