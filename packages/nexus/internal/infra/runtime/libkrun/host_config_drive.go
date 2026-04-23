package libkrun

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// buildHostConfigDriveLibkrun creates a 32 MiB ext4 image with the host user's
// config files injected (gitconfig, SSH keys, tool auth, DNS resolver).
// The same approach as the firecracker package — no root required.
func buildHostConfigDriveLibkrun(home, destPath string) error {
	const sizeMiB = 32

	if err := os.Truncate(destPath, int64(sizeMiB)*1024*1024); err != nil {
		f, err2 := os.Create(destPath)
		if err2 != nil {
			return fmt.Errorf("create config drive: %w", err2)
		}
		if err2 := f.Truncate(int64(sizeMiB) * 1024 * 1024); err2 != nil {
			_ = f.Close()
			return fmt.Errorf("size config drive: %w", err2)
		}
		_ = f.Close()
	}

	if out, err := exec.Command("mkfs.ext4", "-F", "-L", "nexus-host-config", destPath).CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4: %w: %s", err, strings.TrimSpace(string(out)))
	}

	type entry struct{ src, dst string }
	var files []entry

	add := func(src, dst string) {
		if _, err := os.Stat(src); err == nil {
			files = append(files, entry{src, dst})
		}
	}

	add(home+"/.gitconfig", ".gitconfig")
	add(home+"/.ssh/known_hosts", ".ssh/known_hosts")
	add(home+"/.ssh/config", ".ssh/config")
	add(home+"/.config/gh/hosts.yml", ".config/gh/hosts.yml")
	add(home+"/.config/gh/config.yml", ".config/gh/config.yml")
	add(home+"/.config/opencode/opencode.json", ".config/opencode/opencode.json")
	add(home+"/.config/opencode/ocx.jsonc", ".config/opencode/ocx.jsonc")
	add(home+"/.opencode/opencode.json", ".opencode/opencode.json")
	add(home+"/.opencode/opencode.jsonc", ".opencode/opencode.jsonc")
	add(home+"/.config/claude/credentials.json", ".config/claude/credentials.json")
	add(home+"/.config/claude/settings.json", ".config/claude/settings.json")
	add("/etc/resolv.conf", ".resolv.conf")

	if authMaterial, ok := hostSSHAuthorizedKeysMaterial(home); ok {
		if authPath, cleanup, err := writeTempFileForConfigDrive("nexus-authkeys-*", authMaterial); err == nil {
			defer cleanup()
			files = append(files, entry{authPath, ".ssh/authorized_keys"})
		}
	}

	if len(files) == 0 {
		return nil
	}

	dirs := make(map[string]struct{})
	for _, f := range files {
		if d := filepath.Dir(f.dst); d != "." {
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
		parts := strings.Split(dir, "/")
		for i := range parts {
			_ = run("mkdir", strings.Join(parts[:i+1], "/"))
		}
	}
	for _, f := range files {
		if err := run("write", f.src, f.dst); err != nil {
			log.Printf("[libkrun] host config drive: skipping %s: %v", f.dst, err)
		}
	}
	return nil
}

// hostSSHAuthorizedKeysMaterial returns the SSH public key material for the host user.
func hostSSHAuthorizedKeysMaterial(home string) ([]byte, bool) {
	seen := make(map[string]struct{})
	var lines []string

	addKeyLine := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			return
		}
		for _, prefix := range []string{"ssh-rsa ", "ssh-ed25519 ", "ssh-dss ", "ecdsa-sha2-", "sk-ssh-ed25519 ", "sk-ecdsa-sha2-"} {
			if strings.HasPrefix(line, prefix) {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					key := fields[0] + " " + fields[1]
					if _, dup := seen[key]; !dup {
						seen[key] = struct{}{}
						lines = append(lines, line)
					}
				}
				return
			}
		}
	}

	addFile := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		for _, l := range strings.Split(string(data), "\n") {
			addKeyLine(l)
		}
	}

	addFile(filepath.Join(home, ".ssh", "authorized_keys"))
	for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		addFile(filepath.Join(home, ".ssh", name))
	}
	if entries, err := os.ReadDir(filepath.Join(home, ".ssh")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".pub") {
				addFile(filepath.Join(home, ".ssh", e.Name()))
			}
		}
	}

	if len(lines) == 0 {
		return nil, false
	}
	return []byte(strings.Join(lines, "\n") + "\n"), true
}

func writeTempFileForConfigDrive(pattern string, data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, f.Close()
}
