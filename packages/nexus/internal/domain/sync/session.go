package sync

import "time"

// SyncStatus represents the lifecycle state of a sync session.
type SyncStatus string

const (
	SyncStatusCreating SyncStatus = "creating"
	SyncStatusActive   SyncStatus = "active"
	SyncStatusPaused   SyncStatus = "paused"
	SyncStatusStopped  SyncStatus = "stopped"
	SyncStatusError    SyncStatus = "error"
)

// SyncDirection defines the direction of file synchronization.
type SyncDirection string

const (
	SyncDirectionDown          SyncDirection = "down"          // remote → local
	SyncDirectionUp            SyncDirection = "up"            // local → remote
	SyncDirectionBidirectional SyncDirection = "bidirectional" // two-way safe
)

// SyncStats holds sync statistics.
type SyncStats struct {
	TotalSyncs        int64
	BytesSent         int64
	BytesReceived     int64
	FilesSent         int64
	FilesReceived     int64
	ConflictsResolved int64
}

// SyncSession represents an active sync session between a workspace and a local path.
type SyncSession struct {
	ID          string
	WorkspaceID string
	LocalPath   string
	Status      SyncStatus
	Direction   SyncDirection
	Alpha       string // local path (or remote for reverse sync)
	Beta        string // remote path (or local for reverse sync)
	MutagenID   string // mutagen session identifier
	StartedAt   time.Time
	StoppedAt   *time.Time
	LastSyncAt  *time.Time
	Stats       SyncStats
}
