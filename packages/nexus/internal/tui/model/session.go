package model

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/pty"
)

// PTYSessionManager manages multiple PTY sessions per workspace.
type PTYSessionManager struct {
	sessions map[string]*PTYSessionState // sessionID -> state
	active   string                      // active session ID
}

// PTYSessionState represents the state of a single PTY session.
type PTYSessionState struct {
	ID          string
	Label       string
	WorkspaceID string
	Pane        *pty.PtyPane
	DataCh      <-chan []byte
}

// NewPTYSessionManager creates a new PTYSessionManager.
func NewPTYSessionManager() *PTYSessionManager {
	return &PTYSessionManager{
		sessions: make(map[string]*PTYSessionState),
	}
}

// OpenSession opens a new PTY session for a workspace.
func (m *PTYSessionManager) OpenSession(wsID, label string, cols, rows int) (*PTYSessionState, tea.Cmd) {
	sessionID := fmt.Sprintf("%s-%s", wsID, label)

	pane := pty.NewPtyPane(wsID, sessionID, cols, rows)

	state := &PTYSessionState{
		ID:          sessionID,
		Label:       label,
		WorkspaceID: wsID,
		Pane:        pane,
	}

	m.sessions[sessionID] = state
	m.active = sessionID

	return state, nil
}

// SwitchSession switches to a different session.
func (m *PTYSessionManager) SwitchSession(sessionID string) tea.Cmd {
	if _, ok := m.sessions[sessionID]; ok {
		m.active = sessionID
	}
	return nil
}

// CloseSession closes a session and removes it.
func (m *PTYSessionManager) CloseSession(sessionID string) {
	delete(m.sessions, sessionID)

	if m.active == sessionID {
		// Switch to another session if available
		for id := range m.sessions {
			m.active = id
			break
		}
		if len(m.sessions) == 0 {
			m.active = ""
		}
	}
}

// Active returns the active session.
func (m *PTYSessionManager) Active() *PTYSessionState {
	if m.active == "" {
		return nil
	}
	return m.sessions[m.active]
}

// All returns all sessions.
func (m *PTYSessionManager) All() []*PTYSessionState {
	result := make([]*PTYSessionState, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result
}

// ForWorkspace returns all sessions for a given workspace.
func (m *PTYSessionManager) ForWorkspace(wsID string) []*PTYSessionState {
	result := []*PTYSessionState{}
	for _, s := range m.sessions {
		if s.WorkspaceID == wsID {
			result = append(result, s)
		}
	}
	return result
}
