package firecracker

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// isSSHPublicKeyLine returns true if line looks like a valid SSH public key
// (handles all key types: ssh-rsa, ssh-ed25519, ecdsa-sha2-*, sk-ssh-*, etc.).
func isSSHPublicKeyLine(line string) bool {
	for _, prefix := range []string{
		"ssh-rsa ",
		"ssh-ed25519 ",
		"ssh-dss ",
		"ecdsa-sha2-",
		"sk-ssh-ed25519 ",
		"sk-ecdsa-sha2-",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// hostSSHAuthorizedKeysMaterial builds the authorized_keys content to inject
// into a guest VM.
//
// The primary source is the engine host's own ~/.ssh/authorized_keys — those
// are the developer keys that are already trusted to SSH into the engine, so
// they should be trusted to SSH into the VM too (the VM is behind the engine
// via ProxyJump anyway).
//
// Any additional public key files (id_*.pub, *.pub) found in ~/.ssh/ are
// appended as a fallback so that a freshly-configured host without an
// authorized_keys still works.
func hostSSHAuthorizedKeysMaterial(home string) ([]byte, bool) {
	seen := make(map[string]struct{})
	var lines []string

	addKeyLine := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !isSSHPublicKeyLine(line) {
			return
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return
		}
		key := fields[0] + " " + fields[1]
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		lines = append(lines, line)
	}

	addFile := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(data), "\n") {
			addKeyLine(line)
		}
	}

	// Primary: the host's authorized_keys carries the developer keys already
	// trusted to access the engine. This is what we want in the VM.
	addFile(filepath.Join(home, ".ssh", "authorized_keys"))

	// Fallback: the host's own identity public keys (covers the case where the
	// engine host is a developer laptop with no authorized_keys of its own).
	for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub", "id_ed25519_sk.pub", "id_ecdsa_sk.pub"} {
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

func writeTempFileForConfigDrive(pattern string, data []byte) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	path = f.Name()
	cleanup = func() { _ = os.Remove(path) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

// gatewayTempFile writes the bridge gateway IP to a temp file and returns its
// path so debugfs can write it into the host config drive image.
func gatewayTempFile() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "nexus-gateway-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create gateway temp file: %w", err)
	}
	gw := vmNetworkGatewayIP()
	if _, err := f.WriteString(gw + "\n"); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("write gateway temp file: %w", err)
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// buildHostConfigDrive creates a small ext4 image at destPath populated with
// the host user's config files (gitconfig, SSH public material, tool configs)
// and Nexus runtime parameters (gateway IP for guest networking).
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

	// Nexus runtime: gateway IP so the guest can configure its default route
	// even when the bridge subnet differs from the compiled-in default.
	if gwFile, cleanup, err := gatewayTempFile(); err == nil {
		defer cleanup()
		files = append(files, entry{gwFile, ".nexus-gateway"})
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
	add(home+"/.codex/auth.json", ".codex/auth.json")
	add(home+"/.codex/config.json", ".codex/config.json")

	// Collect AI/LLM API keys from the daemon's environment and write them to
	// a .nexus-env file.  The guest agent sources this in /root/.profile so
	// every shell inside the VM has the keys available without extra login steps.
	if envContent := buildAPIKeyEnvFile(); envContent != "" {
		if envPath, cleanup, err := writeTempFileForConfigDrive("nexus-env-*", []byte(envContent)); err == nil {
			defer cleanup()
			files = append(files, entry{envPath, ".nexus-env"})
		}
	}

	// Host DNS resolver config so the guest can use the same upstream DNS
	// servers (important in networks where public DNS is blocked).
	add("/etc/resolv.conf", ".resolv.conf")

	// Public keys for optional in-VM sshd (Remote-SSH from the Nexus macOS app).
	if authMaterial, ok := hostSSHAuthorizedKeysMaterial(home); ok {
		if authPath, cleanup, err := writeTempFileForConfigDrive("nexus-authkeys-*", authMaterial); err == nil {
			defer cleanup()
			files = append(files, entry{authPath, ".ssh/authorized_keys"})
		}
	}

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

// buildAPIKeyEnvFile collects AI/LLM API keys and tool tokens from the current
// process environment and returns a shell-sourceable export string.
// Only well-known, safe-to-forward variables are included.
func buildAPIKeyEnvFile() string {
	// Known AI/LLM API keys and tool auth tokens worth forwarding.
	known := []string{
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"GEMINI_API_KEY",
		"GOOGLE_API_KEY",
		"GOOGLE_GENERATIVE_AI_API_KEY",
		"MISTRAL_API_KEY",
		"GROQ_API_KEY",
		"COHERE_API_KEY",
		"XAI_API_KEY",
		"OPENROUTER_API_KEY",
		"AZURE_OPENAI_API_KEY",
		"AZURE_OPENAI_ENDPOINT",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_DEFAULT_REGION",
		"GITHUB_TOKEN",
		"GH_TOKEN",
	}

	var lines []string
	for _, key := range known {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			// Shell-quote the value: wrap in single quotes, escape embedded singles.
			escaped := strings.ReplaceAll(val, "'", "'\\''")
			lines = append(lines, fmt.Sprintf("export %s='%s'", key, escaped))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
