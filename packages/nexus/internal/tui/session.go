package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// sessionState is persisted to disk between TUI invocations.
type sessionState struct {
	// Tabs holds workspace IDs attached at least once this session, in order.
	Tabs []string `json:"tabs"`
	// Active is the ID of the last workspace attached — used by --auto-attach.
	Active string `json:"active"`
}

// sessionStateFile returns the path of the session persistence file,
// honouring $XDG_STATE_HOME (fallback: ~/.local/state).
func sessionStateFile() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "nexus", "tui-sessions.json")
}

// loadSessionState reads the persisted session state. Returns an empty state
// on any error (missing file, invalid JSON, etc.).
func loadSessionState() sessionState {
	data, err := os.ReadFile(sessionStateFile())
	if err != nil {
		return sessionState{}
	}
	var s sessionState
	if err := json.Unmarshal(data, &s); err != nil {
		return sessionState{}
	}
	return s
}

// saveSessionState atomically writes the session state to disk.
// Errors are silently ignored — session state is best-effort.
func saveSessionState(s sessionState) {
	path := sessionStateFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// addTab appends wsID to tabs (up to maxTabs), evicting the oldest entry if
// full. Returns the new tab slice. If wsID is already present, returns tabs
// unchanged.
func addTab(tabs []string, wsID string) []string {
	const maxTabs = 9
	for _, id := range tabs {
		if id == wsID {
			return tabs
		}
	}
	tabs = append(tabs, wsID)
	if len(tabs) > maxTabs {
		tabs = tabs[len(tabs)-maxTabs:]
	}
	return tabs
}
