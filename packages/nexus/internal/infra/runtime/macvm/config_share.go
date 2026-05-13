//go:build darwin

package macvm

import (
	"fmt"
	"os"
	"path/filepath"

	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// buildConfigShare creates a tmpdir with config files to be shared into the VM
// via virtio-fs at /run/nexus-host/. Contents mirror what the Linux libkrun
// config drive provides: SSH authorized_keys, env vars, and git config.
// The caller is responsible for removing the directory when the VM stops.
func buildConfigShare(ws *domainws.Workspace) (string, error) {
	dir, err := os.MkdirTemp("", "nexus-macvm-config-*")
	if err != nil {
		return "", fmt.Errorf("create config tmpdir: %w", err)
	}

	home, _ := os.UserHomeDir()
	srcAuth := filepath.Join(home, ".ssh", "authorized_keys")
	if data, err := os.ReadFile(srcAuth); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), data, 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("write authorized_keys: %w", err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "workspace-id"), []byte(ws.ID), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write workspace-id: %w", err)
	}

	return dir, nil
}
