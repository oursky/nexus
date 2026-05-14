package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type bootstrapPhaseEvent struct {
	Phase   string `json:"phase"`
	Status  string `json:"status"` // "start" | "ok" | "error"
	Message string `json:"message,omitempty"`
}

func emitPhase(w io.Writer, emitJSON bool, phase, status, msg string) {
	if emitJSON {
		ev := bootstrapPhaseEvent{Phase: phase, Status: status, Message: msg}
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "%s\n", data)
	} else if msg != "" {
		fmt.Fprintf(w, "[%s] %s: %s\n", phase, status, msg)
	} else {
		fmt.Fprintf(w, "[%s] %s\n", phase, status)
	}
}

// nexusDataShareDir resolves ~/.local/share/nexus or $XDG_DATA_HOME/nexus.
func nexusDataShareDir() string {
	if s := os.Getenv("XDG_DATA_HOME"); s != "" {
		return filepath.Join(s, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus")
}

func needsInstall(dest string, newContent []byte) bool {
	existing, err := os.ReadFile(dest)
	if err != nil {
		return true
	}
	return !bytes.Equal(existing, newContent)
}

// writeFileAtomic writes data to path atomically (write to .tmp, then rename).
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmpFile, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func atomicWriteFile(dest string, data []byte, mode os.FileMode) error {
	return writeFileAtomic(dest, data, mode)
}

func atomicWriteExec(dest string, data []byte) error {
	return atomicWriteFile(dest, data, 0o755)
}
