//go:build linux

package libkrun

import (
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func (m *Manager) monitorInstance(workspaceID string, inst *Instance) {
	if inst == nil || inst.Process == nil {
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Signal 0 checks liveness without delivering a real signal.
		if err := inst.Process.Signal(syscall.Signal(0)); err != nil {
			log.Printf("[libkrun] workspace %s: vm process no longer alive: %v", workspaceID, err)
			if inst.PasstProcess != nil {
				if pErr := inst.PasstProcess.Signal(syscall.Signal(0)); pErr == nil {
					_ = inst.PasstProcess.Kill()
				}
			}
			_ = os.Remove(filepath.Join(inst.WorkDir, libkrunPIDFileName))
			_ = os.Remove(filepath.Join(inst.WorkDir, passtPIDFileName))

			m.mu.Lock()
			current, ok := m.instances[workspaceID]
			if ok && current == inst {
				delete(m.instances, workspaceID)
			}
			m.mu.Unlock()
			return
		}

		if inst.PasstProcess != nil {
			if pErr := inst.PasstProcess.Signal(syscall.Signal(0)); pErr != nil {
				log.Printf("[libkrun] workspace %s: passt process no longer alive: %v", workspaceID, pErr)
				// If passt dies, networking is irrecoverable for this VM (the shared
				// socketpair endpoint is gone). Kill the VM so callers get a clean
				// restart path instead of hanging on a half-dead instance.
				_ = inst.Process.Kill()
				_ = os.Remove(filepath.Join(inst.WorkDir, libkrunPIDFileName))
				_ = os.Remove(filepath.Join(inst.WorkDir, passtPIDFileName))

				m.mu.Lock()
				current, ok := m.instances[workspaceID]
				if ok && current == inst {
					delete(m.instances, workspaceID)
				}
				m.mu.Unlock()
				return
			}
		}
	}
}
