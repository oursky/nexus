//go:build linux

package libkrun

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// dumpSerialLog reads the VM process log (libkrun.log) and emits it to the
// daemon's stderr so it appears in CI output even after the workdir is cleaned up.
func dumpSerialLog(workspaceID, workDir string) {
	logPath := filepath.Join(workDir, "libkrun.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		log.Printf("[libkrun] workspace %s: serial log unavailable: %v", workspaceID, err)
		return
	}
	if len(bytes.TrimSpace(data)) == 0 {
		log.Printf("[libkrun] workspace %s: serial log empty", workspaceID)
		return
	}
	log.Printf("[libkrun] workspace %s: serial log:\n%s", workspaceID, data)
}

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
			dumpSerialLog(workspaceID, inst.WorkDir)
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
				dumpSerialLog(workspaceID, inst.WorkDir)
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
