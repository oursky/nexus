package tui

import (
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/clientstate"
)

// sessionState is persisted to SQLite between TUI invocations.
type sessionState struct {
	// Tabs holds workspace IDs attached at least once, ordered by tab_order.
	Tabs []string
	// Active is the ID of the last workspace attached — used by --auto-attach.
	Active string
}

// loadSessionState reads the persisted session state from the client state DB.
// Returns an empty state on any error — session state is best-effort.
func loadSessionState() sessionState {
	db, err := clientstate.Open()
	if err != nil {
		return sessionState{}
	}
	defer db.Close()

	sessions, err := db.GetTUISessions()
	if err != nil {
		return sessionState{}
	}
	tabs := make([]string, 0, len(sessions))
	for _, s := range sessions {
		tabs = append(tabs, s.WorkspaceID)
	}
	active, _ := db.GetTUIPref("active_workspace")
	return sessionState{Tabs: tabs, Active: active}
}

// saveSessionState writes the session state to the client state DB.
// Errors are silently ignored — session state is best-effort.
func saveSessionState(s sessionState) {
	db, err := clientstate.Open()
	if err != nil {
		return
	}
	defer db.Close()

	sessions := make([]clientstate.TUISession, len(s.Tabs))
	for i, id := range s.Tabs {
		sessions[i] = clientstate.TUISession{
			WorkspaceID:  id,
			LastAttached: time.Now().UTC(),
			TabOrder:     i,
		}
	}
	_ = db.ReplaceAllTUISessions(sessions)
	if s.Active != "" {
		_ = db.SetTUIPref("active_workspace", s.Active)
	}
}

// addTab appends wsID to tabs (up to maxTabs), evicting the oldest entry if
// full. Returns the new tab slice unchanged if wsID is already present.
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
