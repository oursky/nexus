package sync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mockMutagenClient is a test double for MutagenClientInterface.
type mockMutagenClient struct {
	mu       sync.Mutex
	statuses map[string]string
	errors   map[string]error
	callCount map[string]int
}

func newMockMutagenClient() *mockMutagenClient {
	return &mockMutagenClient{
		statuses:  make(map[string]string),
		errors:    make(map[string]error),
		callCount: make(map[string]int),
	}
}

func (m *mockMutagenClient) SessionStatus(_ context.Context, sessionID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount[sessionID]++

	if err, ok := m.errors[sessionID]; ok {
		return "", err
	}
	if status, ok := m.statuses[sessionID]; ok {
		return status, nil
	}
	return "", fmt.Errorf("session %q not found", sessionID)
}

func (m *mockMutagenClient) CreateSession(_ context.Context, _, _ string, _ SyncDirection) (string, error) {
	return "", nil
}

func (m *mockMutagenClient) TerminateSession(_ context.Context, _ string) error {
	return nil
}

func (m *mockMutagenClient) SetStatus(sessionID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[sessionID] = status
	delete(m.errors, sessionID)
}

func (m *mockMutagenClient) SetError(sessionID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[sessionID] = err
	delete(m.statuses, sessionID)
}

func (m *mockMutagenClient) GetCallCount(sessionID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount[sessionID]
}

// eventCollector collects monitor events for testing.
type eventCollector struct {
	mu     sync.Mutex
	events []MonitorEvent
}

func (ec *eventCollector) handler(event MonitorEvent) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.events = append(ec.events, event)
}

func (ec *eventCollector) getEvents() []MonitorEvent {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	result := make([]MonitorEvent, len(ec.events))
	copy(result, ec.events)
	return result
}

func (ec *eventCollector) countByType(eventType MonitorEventType) int {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	count := 0
	for _, e := range ec.events {
		if e.Type == eventType {
			count++
		}
	}
	return count
}

func setupTestMonitor(t *testing.T) (*SyncMonitor, *SessionManager, *mockMutagenClient, *eventCollector) {
	t.Helper()

	sessionMgr := NewSessionManager()
	mockClient := newMockMutagenClient()
	collector := &eventCollector{}

	monitor := NewSyncMonitor(sessionMgr, mockClient, 100*time.Millisecond)
	monitor.SetEventHandler(collector.handler)

	return monitor, sessionMgr, mockClient, collector
}

func TestNewSyncMonitor(t *testing.T) {
	sessionMgr := NewSessionManager()
	mockClient := newMockMutagenClient()

	monitor := NewSyncMonitor(sessionMgr, mockClient, 0)
	if monitor == nil {
		t.Fatal("expected non-nil monitor")
	}
	if monitor.interval != 30*time.Second {
		t.Fatalf("expected default interval 30s, got %v", monitor.interval)
	}
	if monitor.maxRetries != 10 {
		t.Fatalf("expected maxRetries=10, got %d", monitor.maxRetries)
	}

	monitor2 := NewSyncMonitor(sessionMgr, mockClient, 5*time.Second)
	if monitor2.interval != 5*time.Second {
		t.Fatalf("expected interval 5s, got %v", monitor2.interval)
	}
}

func TestCheckSession_Healthy(t *testing.T) {
	monitor, sessionMgr, mockClient, collector := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetStatus("mutagen-1", "active")

	status, err := monitor.CheckSession(ctx, "nexus-1")
	if err != nil {
		t.Fatalf("CheckSession failed: %v", err)
	}
	if status != "active" {
		t.Fatalf("expected status=active, got %s", status)
	}

	// Should have emitted SessionHealthy event
	events := collector.getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != SessionHealthy {
		t.Fatalf("expected SessionHealthy event, got %s", events[0].Type)
	}
	if events[0].NexusID != "nexus-1" {
		t.Fatalf("expected nexusID=nexus-1, got %s", events[0].NexusID)
	}
}

func TestCheckSession_SessionNotFound(t *testing.T) {
	monitor, _, _, _ := setupTestMonitor(t)
	ctx := context.Background()

	_, err := monitor.CheckSession(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !errors.Is(err, fmt.Errorf("sync monitor: session %q not found", "nonexistent")) {
		// Error message should contain "not found"
		if err.Error() != fmt.Sprintf("sync monitor: session %q not found", "nonexistent") {
			t.Fatalf("unexpected error message: %v", err)
		}
	}
}

func TestCheckSession_ErrorStatus(t *testing.T) {
	monitor, sessionMgr, mockClient, collector := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetStatus("mutagen-1", "error")

	_, err := monitor.CheckSession(ctx, "nexus-1")
	if err == nil {
		t.Fatal("expected error for error status")
	}

	// Should have emitted SessionError event
	events := collector.getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != SessionError {
		t.Fatalf("expected SessionError event, got %s", events[0].Type)
	}

	// Session should be marked with retry count = 1
	state, ok := monitor.GetSessionState("nexus-1")
	if !ok {
		t.Fatal("expected session state to exist")
	}
	if state.retryCount != 1 {
		t.Fatalf("expected retryCount=1, got %d", state.retryCount)
	}
	if state.isHealthy {
		t.Fatal("expected session to be unhealthy")
	}
}

func TestCheckSession_AutoRetry(t *testing.T) {
	monitor, sessionMgr, mockClient, collector := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")

	// First call returns error
	mockClient.SetError("mutagen-1", errors.New("network error"))

	_, err := monitor.CheckSession(ctx, "nexus-1")
	if err == nil {
		t.Fatal("expected error on first check")
	}

	// Should have SessionError event
	if collector.countByType(SessionError) != 1 {
		t.Fatalf("expected 1 SessionError event, got %d", collector.countByType(SessionError))
	}

	// Second call should be skipped due to backoff (retryCount=1, backoff=1s)
	_, err = monitor.CheckSession(ctx, "nexus-1")
	if err == nil {
		t.Fatal("expected error on second check")
	}

	// Call count should still be 1 (second check skipped due to backoff)
	if mockClient.GetCallCount("mutagen-1") != 1 {
		t.Fatalf("expected 1 mutagen call, got %d", mockClient.GetCallCount("mutagen-1"))
	}

	// Third call should also be skipped
	_, err = monitor.CheckSession(ctx, "nexus-1")
	if err == nil {
		t.Fatal("expected error on third check")
	}
	if mockClient.GetCallCount("mutagen-1") != 1 {
		t.Fatalf("expected 1 mutagen call after third check, got %d", mockClient.GetCallCount("mutagen-1"))
	}

	// Now simulate recovery - set healthy status
	mockClient.SetStatus("mutagen-1", "active")

	// Wait for backoff to expire (1 second base backoff for retryCount=1)
	time.Sleep(1100 * time.Millisecond)

	status, err := monitor.CheckSession(ctx, "nexus-1")
	if err != nil {
		t.Fatalf("CheckSession failed after recovery: %v", err)
	}
	if status != "active" {
		t.Fatalf("expected status=active after recovery, got %s", status)
	}

	// Should have SessionRecovered event
	if collector.countByType(SessionRecovered) != 1 {
		t.Fatalf("expected 1 SessionRecovered event, got %d", collector.countByType(SessionRecovered))
	}

	// State should be healthy again
	state, _ := monitor.GetSessionState("nexus-1")
	if !state.isHealthy {
		t.Fatal("expected session to be healthy after recovery")
	}
	if state.retryCount != 0 {
		t.Fatalf("expected retryCount=0 after recovery, got %d", state.retryCount)
	}
}

func TestCheckSession_MaxRetriesExceeded(t *testing.T) {
	monitor, sessionMgr, mockClient, collector := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")

	// Override max retries for faster test
	monitor.maxRetries = 3

	mockClient.SetError("mutagen-1", errors.New("network error"))

	// Trigger retries
	for i := 0; i < 4; i++ {
		_, _ = monitor.CheckSession(ctx, "nexus-1")
		if i < 3 {
			// Wait for backoff to expire
			time.Sleep(time.Duration(1<<i) * 1100 * time.Millisecond)
		}
	}

	// Should have SessionFailed event after max retries
	if collector.countByType(SessionFailed) != 1 {
		t.Fatalf("expected 1 SessionFailed event, got %d", collector.countByType(SessionFailed))
	}

	// Session should be marked as error in session manager
	if sessionMgr.IsActive("nexus-1") {
		t.Fatal("expected session to be inactive after max retries")
	}
}

func TestCheckSession_PathNotAccessible_FailImmediately(t *testing.T) {
	monitor, sessionMgr, mockClient, collector := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetError("mutagen-1", errors.New("path not accessible: permission denied"))

	_, err := monitor.CheckSession(ctx, "nexus-1")
	if err == nil {
		t.Fatal("expected error for path not accessible")
	}

	// Should immediately emit SessionFailed (not SessionError)
	if collector.countByType(SessionFailed) != 1 {
		t.Fatalf("expected 1 SessionFailed event, got %d", collector.countByType(SessionFailed))
	}
	if collector.countByType(SessionError) != 0 {
		t.Fatalf("expected 0 SessionError events for path error, got %d", collector.countByType(SessionError))
	}

	// Session should be marked as error immediately
	if sessionMgr.IsActive("nexus-1") {
		t.Fatal("expected session to be inactive for path error")
	}
}

func TestCheckSession_WorkspaceNotRunning(t *testing.T) {
	monitor, sessionMgr, mockClient, collector := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetError("mutagen-1", errors.New("workspace is not running"))

	_, err := monitor.CheckSession(ctx, "nexus-1")
	if err == nil {
		t.Fatal("expected error for workspace not running")
	}

	// Should emit SessionError (not SessionFailed)
	if collector.countByType(SessionError) != 1 {
		t.Fatalf("expected 1 SessionError event, got %d", collector.countByType(SessionError))
	}
	if collector.countByType(SessionFailed) != 0 {
		t.Fatalf("expected 0 SessionFailed events, got %d", collector.countByType(SessionFailed))
	}

	// Should not increment retry count for workspace errors
	state, _ := monitor.GetSessionState("nexus-1")
	if state.retryCount != 0 {
		t.Fatalf("expected retryCount=0 for workspace error, got %d", state.retryCount)
	}
}

func TestCheckSession_ThreadSafety(t *testing.T) {
	monitor, sessionMgr, mockClient, _ := setupTestMonitor(t)
	ctx := context.Background()

	// Create multiple sessions
	for i := 0; i < 10; i++ {
		nexusID := fmt.Sprintf("nexus-%d", i)
		mutagenID := fmt.Sprintf("mutagen-%d", i)
		sessionMgr.CreateMapping(nexusID, mutagenID, "ws-1", "vol-1", "/local", "/remote", "bidirectional")
		mockClient.SetStatus(mutagenID, "active")
	}

	// Concurrent checks
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			nexusID := fmt.Sprintf("nexus-%d", idx)
			for j := 0; j < 10; j++ {
				_, _ = monitor.CheckSession(ctx, nexusID)
			}
		}(i)
	}
	wg.Wait()

	// All sessions should be healthy
	for i := 0; i < 10; i++ {
		nexusID := fmt.Sprintf("nexus-%d", i)
		state, ok := monitor.GetSessionState(nexusID)
		if !ok {
			t.Fatalf("expected session state for %s", nexusID)
		}
		if !state.isHealthy {
			t.Fatalf("expected session %s to be healthy", nexusID)
		}
	}
}

func TestStartStop(t *testing.T) {
	monitor, sessionMgr, mockClient, collector := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetStatus("mutagen-1", "active")

	monitor.Start(ctx)

	// Wait for at least one check cycle
	time.Sleep(150 * time.Millisecond)

	monitor.Stop()

	// Should have emitted at least one SessionHealthy event
	if collector.countByType(SessionHealthy) < 1 {
		t.Fatalf("expected at least 1 SessionHealthy event, got %d", collector.countByType(SessionHealthy))
	}

	// After stop, no more events should be emitted
	eventCount := len(collector.getEvents())
	time.Sleep(200 * time.Millisecond)
	if len(collector.getEvents()) != eventCount {
		t.Fatalf("expected no new events after stop, got %d new events", len(collector.getEvents())-eventCount)
	}
}

func TestRemoveSession(t *testing.T) {
	monitor, sessionMgr, mockClient, _ := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetStatus("mutagen-1", "active")

	_, _ = monitor.CheckSession(ctx, "nexus-1")

	state, ok := monitor.GetSessionState("nexus-1")
	if !ok {
		t.Fatal("expected session state to exist")
	}
	if state == nil {
		t.Fatal("expected non-nil session state")
	}

	monitor.RemoveSession("nexus-1")

	_, ok = monitor.GetSessionState("nexus-1")
	if ok {
		t.Fatal("expected session state to be removed")
	}
}

func TestCalculateBackoff(t *testing.T) {
	monitor, _, _, _ := setupTestMonitor(t)

	tests := []struct {
		retryCount int
		expected   time.Duration
	}{
		{0, 1 * time.Second},
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 32 * time.Second},
		{7, 64 * time.Second},
		{8, 128 * time.Second},
		{9, 256 * time.Second},
		{10, 512 * time.Second},
		{11, 512 * time.Second}, // capped at max
	}

	for _, tt := range tests {
		got := monitor.calculateBackoff(tt.retryCount)
		if got != tt.expected {
			t.Errorf("calculateBackoff(%d) = %v, want %v", tt.retryCount, got, tt.expected)
		}
	}
}

func TestIsErrorStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{"active", false},
		{"error", true},
		{"Error", true},
		{"ERROR", true},
		{"conflict", true},
		{"Conflict", true},
		{"halted", true},
		{"paused", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isErrorStatus(tt.status)
		if got != tt.expected {
			t.Errorf("isErrorStatus(%q) = %v, want %v", tt.status, got, tt.expected)
		}
	}
}

func TestIsPathNotAccessibleError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{errors.New("path not accessible"), true},
		{errors.New("permission denied"), true},
		{errors.New("no such file or directory"), true},
		{errors.New("path /foo is not accessible"), true},
		{errors.New("network error"), false},
		{errors.New("workspace not running"), false},
		{nil, false},
	}

	for _, tt := range tests {
		got := isPathNotAccessibleError(tt.err)
		if got != tt.expected {
			t.Errorf("isPathNotAccessibleError(%v) = %v, want %v", tt.err, got, tt.expected)
		}
	}
}

func TestIsWorkspaceNotRunningError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{errors.New("workspace is not running"), true},
		{errors.New("workspace stopped"), true},
		{errors.New("workspace unavailable"), true},
		{errors.New("network error"), false},
		{errors.New("path not accessible"), false},
		{nil, false},
	}

	for _, tt := range tests {
		got := isWorkspaceNotRunningError(tt.err)
		if got != tt.expected {
			t.Errorf("isWorkspaceNotRunningError(%v) = %v, want %v", tt.err, got, tt.expected)
		}
	}
}

func TestCheckSession_StalledSync(t *testing.T) {
	monitor, sessionMgr, mockClient, collector := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetStatus("mutagen-1", "halted")

	_, err := monitor.CheckSession(ctx, "nexus-1")
	if err == nil {
		t.Fatal("expected error for halted status")
	}

	// Should emit SessionError event
	if collector.countByType(SessionError) != 1 {
		t.Fatalf("expected 1 SessionError event, got %d", collector.countByType(SessionError))
	}
}

func TestCheckSession_MutagenIDRefresh(t *testing.T) {
	monitor, sessionMgr, mockClient, _ := setupTestMonitor(t)
	ctx := context.Background()

	sessionMgr.CreateMapping("nexus-1", "mutagen-1", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetStatus("mutagen-1", "active")

	_, _ = monitor.CheckSession(ctx, "nexus-1")

	// Update the mutagen ID in session manager
	sessionMgr.CreateMapping("nexus-1", "mutagen-2", "ws-1", "vol-1", "/local", "/remote", "bidirectional")
	mockClient.SetStatus("mutagen-2", "active")

	status, err := monitor.CheckSession(ctx, "nexus-1")
	if err != nil {
		t.Fatalf("CheckSession failed: %v", err)
	}
	if status != "active" {
		t.Fatalf("expected status=active, got %s", status)
	}

	// State should reflect the new mutagen ID
	state, _ := monitor.GetSessionState("nexus-1")
	if state.mutagenID != "mutagen-2" {
		t.Fatalf("expected mutagenID=mutagen-2, got %s", state.mutagenID)
	}
}
