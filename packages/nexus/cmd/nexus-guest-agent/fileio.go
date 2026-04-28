//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const hostConfigMount = "/run/nexus-host"

// applyHostConfigDrive mounts the host config ext4 image at /run/nexus-host
// and copies known config files into place.
// Device path is resolved from NEXUS_CONFIG_DEV (libkrun) or derived from mode
// (virtiofs layout → /dev/vdd, legacy block layout → /dev/vdc).
func applyHostConfigDrive() error {
	hostConfigDevice := configDevPath()
	if _, err := os.Stat(hostConfigDevice); err != nil {
		return nil // drive not attached — nothing to do
	}

	if err := os.MkdirAll(hostConfigMount, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", hostConfigMount, err)
	}

	if err := unix.Mount(hostConfigDevice, hostConfigMount, "ext4", unix.MS_RDONLY, ""); err != nil {
		if !errors.Is(err, unix.EBUSY) {
			return fmt.Errorf("mount %s: %w", hostConfigDevice, err)
		}
		// Already mounted from a previous call — proceed to copy files.
	}

	type copySpec struct {
		src, dst string
		mode     os.FileMode
	}
	copies := []copySpec{
		{".gitconfig", "/root/.gitconfig", 0o644},
		{".ssh/known_hosts", "/root/.ssh/known_hosts", 0o644},
		{".ssh/config", "/root/.ssh/config", 0o600},
		{".ssh/authorized_keys", "/root/.ssh/authorized_keys", 0o600},
		{".resolv.conf", "/etc/resolv.conf", 0o644},
		{".config/gh/hosts.yml", "/root/.config/gh/hosts.yml", 0o600},
		{".config/gh/config.yml", "/root/.config/gh/config.yml", 0o600},
		{".config/opencode/opencode.json", "/root/.config/opencode/opencode.json", 0o644},
		{".config/opencode/ocx.jsonc", "/root/.config/opencode/ocx.jsonc", 0o644},
		{".opencode/opencode.json", "/root/.opencode/opencode.json", 0o644},
		{".opencode/opencode.jsonc", "/root/.opencode/opencode.jsonc", 0o644},
		{".config/claude/credentials.json", "/root/.config/claude/credentials.json", 0o600},
		{".config/claude/settings.json", "/root/.config/claude/settings.json", 0o644},
		// Codex CLI OAuth token.
		{".codex/auth.json", "/root/.codex/auth.json", 0o600},
		{".codex/config.json", "/root/.codex/config.json", 0o644},
		// opencode provider OAuth tokens (Copilot, OpenAI, etc.).
		{".local/share/opencode/auth.json", "/root/.local/share/opencode/auth.json", 0o600},
	}

	// Ensure /root and /root/.ssh are owned by uid 0 (root). The rootfs may
	// have been built by a non-root unsquashfs extraction which assigns the
	// builder's UID to all files. SSHd refuses to honour authorized_keys
	// inside a directory not owned by the authenticating user.
	_ = os.MkdirAll("/root/.ssh", 0o700)
	_ = os.Chown("/root", 0, 0)
	_ = os.Chown("/root/.ssh", 0, 0)

	for _, c := range copies {
		src := filepath.Join(hostConfigMount, c.src)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // file not in image — skip
		}
		if err := os.MkdirAll(filepath.Dir(c.dst), 0o755); err != nil {
			continue
		}
		// Remove any broken symlink at the destination before writing
		// (Ubuntu minimal cloud image ships /etc/resolv.conf as a dangling
		// symlink; os.WriteFile would silently fail through it).
		if fi, lerr := os.Lstat(c.dst); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(c.dst)
		}
		_ = os.WriteFile(c.dst, data, c.mode)
	}

	// Ensure git safe.directory for the workspace.
	_ = exec.Command("git", "config", "--global", "--add", "safe.directory", "/workspace").Run()
	_ = exec.Command("git", "config", "--global", "--add", "safe.directory", "*").Run()
	// Strip platform-specific credential helpers that won't work in the VM.
	_ = exec.Command("git", "config", "--global", "--unset-all", "credential.helper").Run()

	// Write SSH_AUTH_SOCK and API key env vars to profile so all shells pick
	// them up.  .nexus-env contains export statements for AI/LLM API keys
	// collected from the daemon host's environment.
	profileLines := "\nexport SSH_AUTH_SOCK=/tmp/ssh-agent.sock\n"
	profileLines += "[ -f /run/nexus-host/.nexus-env ] && . /run/nexus-host/.nexus-env\n"
	profileLines += "export PATH=\"$HOME/.local/share/mise/shims:$HOME/.local/bin:$PATH\"\n"
	profileLines += "if [ -x \"$HOME/.local/bin/mise\" ]; then\n"
	profileLines += "  eval \"$($HOME/.local/bin/mise activate bash)\"\n"
	profileLines += "  if [ -d /workspace ]; then (cd /workspace && $HOME/.local/bin/mise trust -a >/dev/null 2>&1 || true); fi\n"
	profileLines += "fi\n"
	f, err := os.OpenFile("/root/.profile", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(profileLines)
		f.Close()
	}

	// Also source immediately into the current process environment so that
	// docker, npm install, and background services inherit the keys.
	if envData, err := os.ReadFile(filepath.Join(hostConfigMount, ".nexus-env")); err == nil {
		for _, line := range strings.Split(string(envData), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "export ") {
				continue
			}
			kv := strings.TrimPrefix(line, "export ")
			idx := strings.IndexByte(kv, '=')
			if idx < 0 {
				continue
			}
			key := kv[:idx]
			val := kv[idx+1:]
			// Strip surrounding single quotes added by shell quoting.
			if len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'' {
				val = strings.ReplaceAll(val[1:len(val)-1], "'\\''", "'")
			}
			_ = os.Setenv(key, val)
		}
	}

	return nil
}
