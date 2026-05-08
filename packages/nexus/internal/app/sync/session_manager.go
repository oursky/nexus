package sync

import (
	"sync"
	"time"
)

// SessionMapping tracks the relationship between Nexus session IDs and mutagen session IDs.
type SessionMapping struct {
	NexusID     string
	MutagenID   string
	WorkspaceID string
	VolumeID    string
	LocalPath   string
	RemotePath  string
	Direction   string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastSyncAt  *time.Time
	Error       string
}

// SessionManager maps Nexus session IDs to mutagen session IDs and tracks metadata.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*SessionMapping // key: NexusID
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*SessionMapping),
	}
}

// CreateMapping registers a new session mapping.
func (m *SessionManager) CreateMapping(nexusID, mutagenID, workspaceID, volumeID, localPath, remotePath, direction string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[nexusID] = &SessionMapping{
		NexusID:     nexusID,
		MutagenID:   mutagenID,
		WorkspaceID: workspaceID,
		VolumeID:    volumeID,
		LocalPath:   localPath,
		RemotePath:  remotePath,
		Direction:   direction,
		Status:      "active",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

// GetMutagenID returns the mutagen session ID for a Nexus session ID.
func (m *SessionManager) GetMutagenID(nexusID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[nexusID]
	if !ok {
		return "", false
	}
	return s.MutagenID, true
}

// GetNexusID returns the Nexus session ID for a mutagen session ID.
func (m *SessionManager) GetNexusID(mutagenID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if s.MutagenID == mutagenID {
			return s.NexusID, true
		}
	}
	return "", false
}

// Remove deletes a session mapping.
func (m *SessionManager) Remove(nexusID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, nexusID)
}

// ListAll returns all session mappings.
func (m *SessionManager) ListAll() []*SessionMapping {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*SessionMapping, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result
}

// IsActive returns true if the session is active.
func (m *SessionManager) IsActive(nexusID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[nexusID]
	if !ok {
		return false
	}
	return s.Status == "active"
}

// UpdateStatus updates the status of a session.
func (m *SessionManager) UpdateStatus(nexusID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[nexusID]; ok {
		s.Status = status
		s.UpdatedAt = time.Now()
	}
}

// UpdateError updates the error message of a session.
func (m *SessionManager) UpdateError(nexusID, err string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[nexusID]; ok {
		s.Error = err
		s.UpdatedAt = time.Now()
	}
}
