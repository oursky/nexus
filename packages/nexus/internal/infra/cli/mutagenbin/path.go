package mutagenbin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var (
	pathOnce sync.Once
	resPath  string
	resErr   error
)

// Path returns an absolute path to the mutagen CLI: the embedded binary on
// supported Darwin/Linux builds, otherwise mutagen from PATH.
// On embedded builds it also extracts mutagen-agents.tar.gz to the same
// directory so Mutagen can install agents on remote hosts.
func Path() (string, error) {
	pathOnce.Do(func() {
		resPath, resErr = resolvePath()
	})
	return resPath, resErr
}

func resolvePath() (string, error) {
	if len(embeddedMutagen) == 0 {
		p, err := exec.LookPath("mutagen")
		if err != nil {
			return "", fmt.Errorf("mutagen not found in PATH and not embedded in this build (supported embeds: macOS and Linux amd64/arm64); install mutagen or run task mutagen:update and rebuild")
		}
		return p, nil
	}

	sum := sha256.Sum256(embeddedMutagen)
	id := hex.EncodeToString(sum[:])
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("mutagen cache dir: %w", err)
	}
	dir := filepath.Join(cacheRoot, "nexus", "mutagen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mutagen cache mkdir: %w", err)
	}

	dest := filepath.Join(dir, "mutagen-"+id+".bin")
	if fi, err := os.Stat(dest); err != nil || fi.Size() != int64(len(embeddedMutagen)) {
		if err := atomicWrite(dest, embeddedMutagen, 0o755); err != nil {
			return "", fmt.Errorf("install mutagen binary: %w", err)
		}
	} else {
		_ = os.Chmod(dest, 0o755)
	}

	// Mutagen searches for mutagen-agents.tar.gz in the directory of the binary.
	// Extract it alongside the binary (keyed by binary hash so it stays in sync).
	if len(embeddedMutagenAgents) > 0 {
		agentsPath := filepath.Join(dir, "mutagen-agents.tar.gz")
		if _, err := os.Stat(agentsPath); err != nil {
			if err := atomicWrite(agentsPath, embeddedMutagenAgents, 0o644); err != nil {
				return "", fmt.Errorf("install mutagen agents: %w", err)
			}
		}
	}

	return dest, nil
}

func atomicWrite(dest string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".mutagen-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		_ = os.Remove(tmpPath)
		// If dest already exists with right size (race), that's fine.
		if fi, statErr := os.Stat(dest); statErr == nil && fi.Size() == int64(len(data)) {
			_ = os.Chmod(dest, mode)
			return nil
		}
		return err
	}
	return nil
}
