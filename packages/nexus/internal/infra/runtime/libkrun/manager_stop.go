//go:build linux

package libkrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Stop terminates a running VM.
func (m *Manager) Stop(_ context.Context, workspaceID string) error {
	m.mu.Lock()
	inst, exists := m.instances[workspaceID]
	if exists {
		delete(m.instances, workspaceID)
	}
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("workspace not found: %s", workspaceID)
	}

	// Gracefully stop VM child process.
	if inst.Process != nil {
		if err := inst.Process.Signal(os.Interrupt); err != nil {
			_ = inst.Process.Kill()
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if err := inst.Process.Signal(syscall.Signal(0)); err != nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err := inst.Process.Signal(syscall.Signal(0)); err == nil {
			_ = inst.Process.Kill()
		}
	}
	if inst.PasstProcess != nil {
		if err := inst.PasstProcess.Signal(os.Interrupt); err != nil {
			_ = inst.PasstProcess.Kill()
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if err := inst.PasstProcess.Signal(syscall.Signal(0)); err != nil {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		if err := inst.PasstProcess.Signal(syscall.Signal(0)); err == nil {
			_ = inst.PasstProcess.Kill()
		}
	}
	_ = os.Remove(filepath.Join(inst.WorkDir, libkrunPIDFileName))
	_ = os.Remove(filepath.Join(inst.WorkDir, passtPIDFileName))
	return nil
}

// Get returns a running instance by workspace ID.
func (m *Manager) Get(workspaceID string) (*Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[workspaceID]
	if !ok {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}
	return inst, nil
}
