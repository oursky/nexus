//go:build darwin

package macvm

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	domainruntime "github.com/oursky/nexus/packages/nexus/internal/domain/runtime"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// Create records a workspace. Disk setup is deferred to Start.
func (m *Manager) Create(_ context.Context, req *domainruntime.CreateRequest) error {
	wsDir := filepath.Join(m.cfg.VMWorkDir, req.WorkspaceID)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return fmt.Errorf("macvm: create workspace dir: %w", err)
	}
	log.Printf("macvm: created workspace dir workspaceID=%s path=%s", req.WorkspaceID, wsDir)
	return nil
}

// Start boots a libkrun VM for the workspace.
// It ensures the base rootfs is present, copies it to a per-workspace location,
// then spawns a libkrun VM process with virtio-fs workspace and config shares.
func (m *Manager) Start(ctx context.Context, ws *domainws.Workspace) error {
	if _, loaded := m.vms.Load(ws.ID); loaded {
		return nil
	}

	if err := ensureRootFS(ctx, m.cfg); err != nil {
		return fmt.Errorf("macvm: ensure rootfs: %w", err)
	}

	wsDir := filepath.Join(m.cfg.VMWorkDir, ws.ID)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return fmt.Errorf("macvm: workspace dir: %w", err)
	}

	wsRootFS := filepath.Join(wsDir, "rootfs.ext4")
	if err := copyFile(m.cfg.RootFSCachePath, wsRootFS); err != nil {
		return fmt.Errorf("macvm: copy rootfs: %w", err)
	}

	configDir, err := buildConfigShare(ws)
	if err != nil {
		return fmt.Errorf("macvm: build config share: %w", err)
	}

	inst, err := spawnVM(ctx, spawnConfig{
		workspaceID:    ws.ID,
		workDir:        wsDir,
		rootFSPath:     wsRootFS,
		workspacePath:  ws.Repo,
		configDir:      configDir,
		libDir:         m.cfg.LibDir,
	})
	if err != nil {
		_ = os.RemoveAll(configDir)
		return fmt.Errorf("macvm: spawn vm: %w", err)
	}

	m.vms.Store(ws.ID, inst)
	log.Printf("macvm: started workspaceID=%s pid=%d sshPort=%d", ws.ID, inst.pid, inst.guestSSHPort)
	return nil
}

// Stop terminates the VM for the workspace.
func (m *Manager) Stop(_ context.Context, ws *domainws.Workspace) error {
	val, loaded := m.vms.LoadAndDelete(ws.ID)
	if !loaded {
		return nil
	}
	inst := val.(*vmInstance)
	if inst.stop != nil {
		inst.stop()
	}
	if inst.done != nil {
		<-inst.done
	}
	log.Printf("macvm: stopped workspaceID=%s", ws.ID)
	return nil
}

// Destroy removes per-workspace VM state.
func (m *Manager) Destroy(ctx context.Context, ws *domainws.Workspace) error {
	_ = m.Stop(ctx, ws)
	wsDir := filepath.Join(m.cfg.VMWorkDir, ws.ID)
	if err := os.RemoveAll(wsDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("macvm: destroy workspace dir: %w", err)
	}
	log.Printf("macvm: destroyed workspaceID=%s", ws.ID)
	return nil
}

// GuestSSHHost returns the host:port for SSH access to a running VM.
func (m *Manager) GuestSSHHost(_ context.Context, workspaceID string) (string, bool) {
	val, ok := m.vms.Load(workspaceID)
	if !ok {
		return "", false
	}
	inst := val.(*vmInstance)
	return fmt.Sprintf("127.0.0.1:%d", inst.guestSSHPort), true
}
