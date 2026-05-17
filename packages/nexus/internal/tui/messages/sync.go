package messages

// SyncRequested is sent when user opens sync panel.
type SyncRequested struct{}

// SyncClosed is sent when sync panel is closed.
type SyncClosed struct{}

// SyncStartRequested is sent when user starts a sync session.
type SyncStartRequested struct {
    WorkspaceID string
}

// SyncPauseRequested is sent when user pauses a sync session.
type SyncPauseRequested struct {
    WorkspaceID string
}

// SyncResumeRequested is sent when user resumes a sync session.
type SyncResumeRequested struct {
    WorkspaceID string
}

// SyncStopRequested is sent when user stops a sync session.
type SyncStopRequested struct {
    WorkspaceID string
}

// SyncStatusReceived is sent when sync status is received.
type SyncStatusReceived struct {
    WorkspaceID string
    Status      string
}
