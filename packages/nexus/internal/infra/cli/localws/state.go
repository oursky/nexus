// Package-level client-side workspace state, stored in
// ~/.local/share/nexus/workspaces.json.
//
// This lets CLI commands like "fork" look up the original Mac-side path for a
// workspace without re-querying the daemon.
package localws

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WorkspaceRecord describes the Mac-side representation of one workspace.
type WorkspaceRecord struct {
	// WorkspaceID is the daemon-assigned ID (e.g. "ws-1776704458406943774").
	WorkspaceID string `json:"workspaceID"`
	// WorkspaceName is the human-readable name.
	WorkspaceName string `json:"workspaceName"`
	// LocalPath is the Mac-side path the user works in.
	// For base workspaces this is the git root itself; for forks it is the
	// worktree directory.
	LocalPath string `json:"localPath"`
	// GitRoot is the root git repository directory (never a worktree).
	// For base workspaces GitRoot == LocalPath.
	// For forks GitRoot is the parent's git root.
	GitRoot string `json:"gitRoot"`
	// IsWorktree is true when LocalPath was created by "git worktree add".
	IsWorktree bool `json:"isWorktree"`
}

// stateFilePath returns the canonical path for the workspace state file.
func stateFilePath() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "nexus", "workspaces.json"), nil
}

// LoadState reads all workspace records from disk.
// Returns an empty map if the file does not exist yet.
func LoadState() (map[string]*WorkspaceRecord, error) {
	path, err := stateFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(map[string]*WorkspaceRecord), nil
	}
	if err != nil {
		return nil, err
	}
	var records map[string]*WorkspaceRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return make(map[string]*WorkspaceRecord), nil
	}
	return records, nil
}

// SaveRecord writes (or overwrites) a single record to the state file.
func SaveRecord(rec WorkspaceRecord) error {
	records, _ := LoadState()
	if records == nil {
		records = make(map[string]*WorkspaceRecord)
	}
	records[rec.WorkspaceID] = &rec

	path, err := stateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// GetRecord looks up a single workspace by ID.
func GetRecord(workspaceID string) (*WorkspaceRecord, bool) {
	records, err := LoadState()
	if err != nil {
		return nil, false
	}
	rec, ok := records[workspaceID]
	return rec, ok
}

// DeleteRecord removes a workspace record by ID (best-effort).
func DeleteRecord(workspaceID string) {
	records, err := LoadState()
	if err != nil {
		return
	}
	delete(records, workspaceID)
	path, err := stateFilePath()
	if err != nil {
		return
	}
	data, _ := json.MarshalIndent(records, "", "  ")
	_ = os.WriteFile(path, data, 0o600)
}
