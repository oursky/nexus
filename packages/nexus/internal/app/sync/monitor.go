package sync

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	domainsync "github.com/oursky/nexus/packages/nexus/internal/domain/sync"
)

// MonitorEventType represents the type of event emitted by SyncMonitor.
type MonitorEventType string

const (
	// SessionHealthy indicates a session is healthy and syncing normally.
	SessionHealthy MonitorEventType = "session_healthy"
	// SessionError indicates a session has encountered an error state.
	SessionError MonitorEventType = "session_error"
	// SessionRecovered indicates a session has recovered from an error state.
	SessionRecovered MonitorEventType = "session_recovered"
	// SessionFailed indicates a session has permanently failed after retries.
	SessionFailed MonitorEventType = "session_failed"
)

// MonitorEvent represents an event emitted by the SyncMonitor.
type MonitorEvent struct {
	Type      MonitorEventType
	NexusID   string
	MutagenID string
	Error     error
	Timestamp time.Time
}

// MonitorEventHandler is called when a monitor event occurs.
type MonitorEventHandler func(event MonitorEvent)

// sessionState tracks the internal monitoring state for a single session.
type sessionState struct {
	nexusID     string
	mutagenID   string
	lastStatus  string
	lastCheckAt time.Time
	retryCount  int
	lastError   error
	isHealthy   bool
}

// SyncMonitor periodically checks the health of active sync sessions and
// handles error recovery with exponential backoff.
type SyncMonitor struct {
	mu          sync.RWMutex
	sessionMgr  *SessionManager
	mutagen     MutagenClientInterface
	interval    time.Duration
	stopCh      chan struct{}
	wg          sync.WaitGroup
	handler     MonitorEventHandler
	handlerMu   sync.RWMutex             // separate mutex for handler to avoid deadlock
	states      map[string]*sessionState // key: nexusID
	maxRetries  int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

// NewSyncMonitor creates a new SyncMonitor.
//
// sessionManager: tracks active sessions and their mutagen IDs.
// mutagenClient:  used to query session status from mutagen.
// interval:       how often to check all sessions (default 30s if <= 0).
func NewSyncMonitor(sessionManager *SessionManager, mutagenClient MutagenClientInterface, interval time.Duration) *SyncMonitor {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &SyncMonitor{
		sessionMgr:  sessionManager,
		mutagen:     mutagenClient,
		interval:    interval,
		stopCh:      make(chan struct{}),
		states:      make(map[string]*sessionState),
		maxRetries:  10,
		baseBackoff: 1 * time.Second,
		maxBackoff:  512 * time.Second,
	}
}

// SetEventHandler sets the event handler for monitor events.
// Must be called before Start.
func (m *SyncMonitor) SetEventHandler(handler MonitorEventHandler) {
	m.handlerMu.Lock()
	defer m.handlerMu.Unlock()
	m.handler = handler
}

// Start begins monitoring sessions in a background goroutine.
func (m *SyncMonitor) Start(ctx context.Context) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.run(ctx)
	}()
}

// Stop stops the monitor and waits for the background goroutine to finish.
func (m *SyncMonitor) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// run is the main monitoring loop.
func (m *SyncMonitor) run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	// Initial check immediately
	m.checkAllSessions(ctx)

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkAllSessions(ctx)
		}
	}
}

// checkAllSessions checks all sessions managed by the SessionManager.
func (m *SyncMonitor) checkAllSessions(ctx context.Context) {
	sessions := m.sessionMgr.ListAll()

	for _, sess := range sessions {
		if sess.Status == "stopped" {
			continue
		}

		_, _ = m.CheckSession(ctx, sess.NexusID)
	}
}

// CheckSession checks the health of a single session by its NexusID.
// It queries mutagen for the session status, detects errors, and handles
// retry logic. Returns the current status string or an empty string if
// the session is not found.
func (m *SyncMonitor) CheckSession(ctx context.Context, nexusID string) (string, error) {
	mutagenID, ok := m.sessionMgr.GetMutagenID(nexusID)
	if !ok {
		return "", fmt.Errorf("sync monitor: session %q not found", nexusID)
	}

	m.mu.Lock()
	state, exists := m.states[nexusID]
	if !exists {
		state = &sessionState{
			nexusID:   nexusID,
			mutagenID: mutagenID,
			isHealthy: true,
		}
		m.states[nexusID] = state
	}
	m.mu.Unlock()

	// Check if we should skip this check due to backoff
	if !m.shouldCheckNow(state) {
		return state.lastStatus, state.lastError
	}

	status, err := m.mutagen.SessionStatus(ctx, mutagenID)

	m.mu.Lock()
	defer m.mu.Unlock()

	state.lastCheckAt = time.Now()
	state.mutagenID = mutagenID // refresh in case it changed

	if err != nil {
		return m.handleError(state, err)
	}

	state.lastStatus = status

	// Check for error status from mutagen
	if isErrorStatus(status) {
		return m.handleError(state, fmt.Errorf("mutagen session status: %s", status))
	}

	// Session is healthy
	if !state.isHealthy {
		// Recovered from error
		state.isHealthy = true
		state.retryCount = 0
		state.lastError = nil
		m.emitEvent(MonitorEvent{
			Type:      SessionRecovered,
			NexusID:   nexusID,
			MutagenID: mutagenID,
			Timestamp: time.Now(),
		})
	}

	state.isHealthy = true
	state.retryCount = 0
	state.lastError = nil

	m.emitEvent(MonitorEvent{
		Type:      SessionHealthy,
		NexusID:   nexusID,
		MutagenID: mutagenID,
		Timestamp: time.Now(),
	})

	return status, nil
}

// shouldCheckNow returns true if the session should be checked now,
// respecting the retry backoff.
func (m *SyncMonitor) shouldCheckNow(state *sessionState) bool {
	if state.isHealthy || state.retryCount == 0 {
		return true
	}

	backoff := m.calculateBackoff(state.retryCount)
	if time.Since(state.lastCheckAt) < backoff {
		return false
	}
	return true
}

// calculateBackoff computes the exponential backoff duration.
func (m *SyncMonitor) calculateBackoff(retryCount int) time.Duration {
	backoff := m.baseBackoff
	for i := 1; i < retryCount && i < 10; i++ {
		backoff *= 2
		if backoff >= m.maxBackoff {
			backoff = m.maxBackoff
			break
		}
	}
	return backoff
}

// handleError processes an error detected during session checking.
// It implements the retry logic and emits appropriate events.
func (m *SyncMonitor) handleError(state *sessionState, err error) (string, error) {
	state.lastError = err

	// Check for path-not-accessible errors - fail immediately
	if isPathNotAccessibleError(err) {
		state.isHealthy = false
		m.sessionMgr.UpdateStatus(state.nexusID, string(domainsync.SyncStatusError))
		m.sessionMgr.UpdateError(state.nexusID, err.Error())
		m.emitEvent(MonitorEvent{
			Type:      SessionFailed,
			NexusID:   state.nexusID,
			MutagenID: state.mutagenID,
			Error:     err,
			Timestamp: time.Now(),
		})
		return "", err
	}

	// Check for workspace-not-running errors - wait for workspace start
	if isWorkspaceNotRunningError(err) {
		// Don't count as a retry, just wait
		state.isHealthy = false
		m.emitEvent(MonitorEvent{
			Type:      SessionError,
			NexusID:   state.nexusID,
			MutagenID: state.mutagenID,
			Error:     err,
			Timestamp: time.Now(),
		})
		return "", err
	}

	// Network or other errors - retry with exponential backoff
	state.retryCount++

	if state.retryCount > m.maxRetries {
		// Max retries exceeded - mark as failed
		state.isHealthy = false
		m.sessionMgr.UpdateStatus(state.nexusID, string(domainsync.SyncStatusError))
		m.sessionMgr.UpdateError(state.nexusID, fmt.Sprintf("max retries exceeded: %v", err))
		m.emitEvent(MonitorEvent{
			Type:      SessionFailed,
			NexusID:   state.nexusID,
			MutagenID: state.mutagenID,
			Error:     err,
			Timestamp: time.Now(),
		})
		return "", fmt.Errorf("sync monitor: session %s failed after %d retries: %w", state.nexusID, m.maxRetries, err)
	}

	state.isHealthy = false
	m.emitEvent(MonitorEvent{
		Type:      SessionError,
		NexusID:   state.nexusID,
		MutagenID: state.mutagenID,
		Error:     err,
		Timestamp: time.Now(),
	})

	return "", err
}

// isErrorStatus returns true if the mutagen status indicates an error.
func isErrorStatus(status string) bool {
	lower := strings.ToLower(status)
	return lower == "error" || lower == "conflict" || lower == "halted"
}

// isPathNotAccessibleError returns true if the error indicates the path is not accessible.
func isPathNotAccessibleError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "path") &&
		(strings.Contains(errStr, "not accessible") ||
			strings.Contains(errStr, "permission denied") ||
			strings.Contains(errStr, "no such file")) ||
		strings.Contains(errStr, "permission denied") ||
		strings.Contains(errStr, "no such file")
}

// isWorkspaceNotRunningError returns true if the error indicates the workspace is not running.
func isWorkspaceNotRunningError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "workspace") &&
		(strings.Contains(errStr, "not running") ||
			strings.Contains(errStr, "stopped") ||
			strings.Contains(errStr, "unavailable"))
}

// emitEvent emits a monitor event if a handler is registered.
func (m *SyncMonitor) emitEvent(event MonitorEvent) {
	m.handlerMu.RLock()
	handler := m.handler
	m.handlerMu.RUnlock()

	if handler != nil {
		handler(event)
	}
}

// GetSessionState returns the current monitoring state for a session.
// Used primarily for testing.
func (m *SyncMonitor) GetSessionState(nexusID string) (*sessionState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.states[nexusID]
	return state, ok
}

// RemoveSession removes a session from monitoring.
func (m *SyncMonitor) RemoveSession(nexusID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, nexusID)
}
