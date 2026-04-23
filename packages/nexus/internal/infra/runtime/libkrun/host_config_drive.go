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
// Uses mke2fs -d to build from a staging directory, which produces a kernel-readable
// ext4 image with correct checksums (unlike debugfs -w which skips metadata_csum).
func buildHostConfigDriveLibkrun(home, destPath string) error {
	const sizeMiB = 32

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
	add(home+"/.local/share/opencode/auth.json", ".local/share/opencode/auth.json")
	add(home+"/.config/claude/credentials.json", ".config/claude/credentials.json")
	add(home+"/.config/claude/settings.json", ".config/claude/settings.json")
	add(home+"/.codex/auth.json", ".codex/auth.json")
	add(home+"/.codex/config.json", ".codex/config.json")
	add("/etc/resolv.conf", ".resolv.conf")

	// AI/LLM API keys from daemon process environment → sourced by guest /root/.profile.
	if envContent := buildAPIKeyEnvFileLibkrun(); envContent != "" {
		if envPath, cleanup, err := writeTempFileForConfigDrive("nexus-env-*", []byte(envContent)); err == nil {
			defer cleanup()
			files = append(files, entry{envPath, ".nexus-env"})
		}
	}

	if authMaterial, ok := hostSSHAuthorizedKeysMaterial(home); ok {
		if authPath, cleanup, err := writeTempFileForConfigDrive("nexus-authkeys-*", authMaterial); err == nil {
			defer cleanup()
			files = append(files, entry{authPath, ".ssh/authorized_keys"})
		}
	}

	// Build a staging directory to pass to mke2fs -d.
	stagingDir, err := os.MkdirTemp("", "nexus-host-config-*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	for _, f := range files {
		dstFull := filepath.Join(stagingDir, f.dst)
		if err := os.MkdirAll(filepath.Dir(dstFull), 0o755); err != nil {
			log.Printf("[libkrun] host config drive: mkdir %s: %v", filepath.Dir(f.dst), err)
			continue
		}
		data, err := os.ReadFile(f.src)
		if err != nil {
			log.Printf("[libkrun] host config drive: read %s: %v", f.src, err)
			continue
		}
		// Preserve source permissions but ensure files are readable.
		info, _ := os.Stat(f.src)
		perm := os.FileMode(0o644)
		if info != nil {
			perm = info.Mode().Perm()
		}
		if err := os.WriteFile(dstFull, data, perm); err != nil {
			log.Printf("[libkrun] host config drive: write %s: %v", f.dst, err)
		}
	}

	// Create the ext4 image from the staging directory.
	// mke2fs -d properly updates directory entries and checksums.
	szBytes := int64(sizeMiB) * 1024 * 1024
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o664)
	if err != nil {
		return fmt.Errorf("create config drive file: %w", err)
	}
	if err := f.Truncate(szBytes); err != nil {
		_ = f.Close()
		return fmt.Errorf("size config drive: %w", err)
	}
	_ = f.Close()

	args := []string{
		"-t", "ext4",
		"-L", "nexus-host-config",
		"-d", stagingDir,
		destPath,
		fmt.Sprintf("%dk", sizeMiB*1024),
	}
	if out, err := exec.Command("mke2fs", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("mke2fs: %w: %s", err, strings.TrimSpace(string(out)))
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

// buildAPIKeyEnvFileLibkrun collects AI/LLM API keys from the daemon's environment
// and returns a shell-sourceable export string for the guest's /root/.profile.
//
// Keys are sourced in priority order (later values win on duplicates):
//  1. Daemon process environment
//  2. ~/.config/nexus/api-keys.env (KEY=value lines, # comments ignored)
//
// The api-keys.env file allows users to persist API keys without requiring
// the daemon to be launched with specific environment variables.
func buildAPIKeyEnvFileLibkrun() string {
	known := []string{
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY",
		"GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY",
		"MISTRAL_API_KEY", "GROQ_API_KEY", "COHERE_API_KEY",
		"XAI_API_KEY", "OPENROUTER_API_KEY",
		"AZURE_OPENAI_API_KEY", "AZURE_OPENAI_ENDPOINT",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_DEFAULT_REGION",
		"GITHUB_TOKEN", "GH_TOKEN",
	}

	// Collect from daemon environment.
	result := map[string]string{}
	for _, key := range known {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			result[key] = val
		}
	}

	// Overlay with ~/.config/nexus/api-keys.env if present.
	if home, err := os.UserHomeDir(); err == nil {
		keysFile := filepath.Join(home, ".config", "nexus", "api-keys.env")
		if data, err := os.ReadFile(keysFile); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				// Strip optional "export " prefix.
				line = strings.TrimPrefix(line, "export ")
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				k = strings.TrimSpace(k)
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				if k != "" && v != "" {
					result[k] = v
				}
			}
		}
	}

	if len(result) == 0 {
		return ""
	}
	var lines []string
	for _, key := range known {
		if val, ok := result[key]; ok {
			escaped := strings.ReplaceAll(val, "'", "'\\''")
			lines = append(lines, fmt.Sprintf("export %s='%s'", key, escaped))
		}
	}
	// Include any extra keys from the file that weren't in the known list.
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		knownSet[k] = true
	}
	for k, v := range result {
		if !knownSet[k] {
			escaped := strings.ReplaceAll(v, "'", "'\\''")
			lines = append(lines, fmt.Sprintf("export %s='%s'", k, escaped))
		}
	}
	return strings.Join(lines, "\n") + "\n"
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
