package sync

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	domainsync "github.com/oursky/nexus/packages/nexus/internal/domain/sync"
	domainws "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// SyncSessionDTO is the data-transfer object for a sync session.
type SyncSessionDTO struct {
	ID                string
	WorkspaceID       string
	LocalPath         string
	Status            string
	Direction         string
	Alpha             string
	Beta              string
	StartedAt         time.Time
	StoppedAt         *time.Time
	LastSyncAt        *time.Time
	TotalSyncs        int64
	BytesSent         int64
	BytesReceived     int64
	FilesSent         int64
	FilesReceived     int64
	ConflictsResolved int64
}

// WorkspaceLookup resolves a workspace to its SSH connection details.
type WorkspaceLookup interface {
	Get(ctx context.Context, id string) (*domainws.Workspace, error)
}

// MutagenClientInterface defines the interface for mutagen sync operations.
// Both MutagenClient (gRPC-based) and EmbeddedMutagenClient (in-process) implement this.
type MutagenClientInterface interface {
	CreateSession(ctx context.Context, alpha, beta string, mode SyncDirection) (string, error)
	TerminateSession(ctx context.Context, sessionID string) error
	SessionStatus(ctx context.Context, sessionID string) (string, error)
	PauseSession(ctx context.Context, sessionID string) error
	ResumeSession(ctx context.Context, sessionID string) error
}

// Service manages volume sync sessions via MutagenClient.
// It implements the SyncStarter interface consumed by the RPC handler.
// mc may be nil, in which case mutagen operations return errors gracefully.
type Service struct {
	mutagen    MutagenClientInterface
	repo       domainsync.Repository
	wsLookup   WorkspaceLookup
	sshManager *SSHManager
	driver     SyncDriver
	store      *SessionStore
}

// NewService returns a Service backed by the given MutagenClient and Repository.
func NewService(mc MutagenClientInterface, repo domainsync.Repository, wsLookup WorkspaceLookup) *Service {
	if wsLookup == nil {
		panic("sync: WorkspaceLookup is required")
	}
	return &Service{
		mutagen:  mc,
		repo:     repo,
		wsLookup: wsLookup,
	}
}

// SetSSHManager wires the SSH manager for cross-host sync.
func (s *Service) SetSSHManager(sm *SSHManager) {
	s.sshManager = sm
}

// SetDriver sets the sync driver for platform-specific path resolution.
func (s *Service) SetDriver(driver SyncDriver) {
	s.driver = driver
}

// SetStore sets the session store for persistence.
func (s *Service) SetStore(store *SessionStore) {
	s.store = store
}

// StartSync starts a new sync session for workspaceID/localPath in the given direction.
func (s *Service) StartSync(ctx context.Context, workspaceID, localPath, direction string) (*SyncSessionDTO, error) {
	dir := domainsync.SyncDirection(direction)
	mode, err := directionToMode(dir)
	if err != nil {
		return nil, err
	}

	ws, err := s.wsLookup.Get(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sync: workspace %q not found: %w", workspaceID, err)
	}

	// Check if workspace already has an active sync.
	existing, _ := s.repo.GetByWorkspace(ctx, workspaceID)
	if existing != nil && existing.Status != domainsync.SyncStatusStopped {
		return nil, fmt.Errorf("sync: workspace %q already has an active sync session %q", workspaceID, existing.ID)
	}

	// Validate local path exists and is accessible.
	if _, err := os.Stat(localPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("sync: local path %q does not exist", localPath)
		}
		return nil, fmt.Errorf("sync: local path %q is not accessible: %w", localPath, err)
	}

	var alpha, beta string
	if s.driver != nil && s.driver.IsAvailable(workspaceID) {
		alpha, beta, err = s.driver.GetSyncPaths(workspaceID)
		if err != nil {
			return nil, fmt.Errorf("sync: driver failed for workspace %q: %w", workspaceID, err)
		}
		// Drivers may leave alpha empty to indicate the caller should supply it.
		if alpha == "" {
			alpha = localPath
		}
	} else {
		// Fall back to workspace-based path resolution
		if ws.GuestIP == "" && ws.RootPath == "" {
			return nil, fmt.Errorf("sync: workspace %q is not running (no guest IP)", workspaceID)
		}
		alpha = localPath
		if ws.GuestIP == "" || ws.GuestIP == "local" {
			beta = ws.RootPath
		} else {
			beta = fmt.Sprintf("user@%s:%s", ws.GuestIP, ws.RootPath)
		}
	}

	if dir == domainsync.SyncDirectionDown {
		alpha, beta = beta, alpha
	}

	mutagenID := ""
	if s.mutagen != nil {
		mutagenID, err = s.mutagen.CreateSession(ctx, alpha, beta, mode)
		if err != nil {
			return nil, fmt.Errorf("sync: create session: %w", err)
		}
	}

	id := mutagenID
	if id == "" {
		id = uuid.New().String()
	}

	sess := &domainsync.SyncSession{
		ID:          id,
		WorkspaceID: workspaceID,
		LocalPath:   localPath,
		Status:      domainsync.SyncStatusActive,
		Direction:   dir,
		Alpha:       alpha,
		Beta:        beta,
		MutagenID:   mutagenID,
		StartedAt:   time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, sess); err != nil {
		return nil, fmt.Errorf("sync: store session: %w", err)
	}

	if s.store != nil {
		if err := s.store.Add(sess); err != nil {
			return nil, fmt.Errorf("sync: persist session: %w", err)
		}
	}

	return s.toDTO(sess), nil
}

// StopSync stops the sync session identified by sessionID or (if empty) workspaceID.
func (s *Service) StopSync(ctx context.Context, sessionID, workspaceID string) error {
	if sessionID != "" {
		sess, err := s.repo.Get(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("sync: session %q not found", sessionID)
		}
		return s.terminate(ctx, sess)
	}

	// stop all sessions for the workspace
	sessions, err := s.repo.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}

	var firstErr error
	for _, sess := range sessions {
		if sess.Status == domainsync.SyncStatusStopped {
			continue
		}
		if err := s.terminate(ctx, sess); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SyncStatus returns the current status of the given session.
func (s *Service) SyncStatus(ctx context.Context, sessionID, workspaceID string) (*SyncSessionDTO, error) {
	id := sessionID
	if id == "" {
		sess, err := s.repo.GetByWorkspace(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("sync: no sessions found for workspace %q", workspaceID)
		}
		id = sess.ID
	}

	sess, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("sync: session %q not found", id)
	}

	if sess.Status == domainsync.SyncStatusStopped {
		return s.toDTO(sess), nil
	}

	// Refresh status from mutagen if available.
	if s.mutagen != nil && sess.MutagenID != "" {
		if st, mutErr := s.mutagen.SessionStatus(ctx, sess.MutagenID); mutErr == nil {
			sess.Status = domainsync.SyncStatus(st)
		}
	}
	return s.toDTO(sess), nil
}

// ListSyncs returns all sessions for the given workspaceID (or all if empty).
func (s *Service) ListSyncs(ctx context.Context, workspaceID string) ([]*SyncSessionDTO, error) {
	sessions, err := s.repo.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	out := make([]*SyncSessionDTO, len(sessions))
	for i, sess := range sessions {
		out[i] = s.toDTO(sess)
	}
	return out, nil
}

// terminate stops a session in mutagen and marks it stopped locally.
func (s *Service) terminate(ctx context.Context, sess *domainsync.SyncSession) error {
	if s.mutagen != nil && sess.MutagenID != "" {
		if err := s.mutagen.TerminateSession(ctx, sess.MutagenID); err != nil {
			return fmt.Errorf("sync: terminate %q: %w", sess.ID, err)
		}
	}
	now := time.Now().UTC()
	sess.Status = domainsync.SyncStatusStopped
	sess.StoppedAt = &now
	if err := s.repo.Update(ctx, sess); err != nil {
		return err
	}
	if s.store != nil {
		if err := s.store.Update(sess); err != nil {
			return fmt.Errorf("sync: persist terminated session: %w", err)
		}
	}
	return nil
}

func (s *Service) toDTO(sess *domainsync.SyncSession) *SyncSessionDTO {
	status := string(sess.Status)
	if status == "" {
		status = "unknown"
	}

	return &SyncSessionDTO{
		ID:                sess.ID,
		WorkspaceID:       sess.WorkspaceID,
		LocalPath:         sess.LocalPath,
		Status:            status,
		Direction:         string(sess.Direction),
		Alpha:             sess.Alpha,
		Beta:              sess.Beta,
		StartedAt:         sess.StartedAt,
		StoppedAt:         sess.StoppedAt,
		LastSyncAt:        sess.LastSyncAt,
		TotalSyncs:        sess.Stats.TotalSyncs,
		BytesSent:         sess.Stats.BytesSent,
		BytesReceived:     sess.Stats.BytesReceived,
		FilesSent:         sess.Stats.FilesSent,
		FilesReceived:     sess.Stats.FilesReceived,
		ConflictsResolved: sess.Stats.ConflictsResolved,
	}
}

func directionToMode(dir domainsync.SyncDirection) (SyncDirection, error) {
	switch dir {
	case "", domainsync.SyncDirectionBidirectional:
		return SyncDirectionBidirectional, nil
	case domainsync.SyncDirectionUp:
		return SyncDirectionUp, nil
	case domainsync.SyncDirectionDown:
		return SyncDirectionDown, nil
	default:
		return 0, fmt.Errorf("sync: unknown direction %q", dir)
	}
}

// RestoreSessions loads persisted sessions from disk and recreates them in the repository.
func (s *Service) RestoreSessions(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	if err := s.store.Load(); err != nil {
		return fmt.Errorf("sync: load persisted sessions: %w", err)
	}
	for _, sess := range s.store.List() {
		if sess.Status == domainsync.SyncStatusStopped {
			continue
		}
		// Mark as error since we can't verify mutagen state after restart
		sess.Status = domainsync.SyncStatusError
		if err := s.repo.Create(ctx, sess); err != nil {
			return fmt.Errorf("sync: restore session %q: %w", sess.ID, err)
		}
		if err := s.store.Update(sess); err != nil {
			return fmt.Errorf("sync: persist restored session %q: %w", sess.ID, err)
		}
	}
	return nil
}

// PauseSync pauses an active sync session.
func (s *Service) PauseSync(ctx context.Context, sessionID string) error {
	sess, err := s.repo.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("sync: session %q not found", sessionID)
	}
	if sess.Status == domainsync.SyncStatusPaused {
		return fmt.Errorf("sync: session %q is already paused", sessionID)
	}
	if sess.Status != domainsync.SyncStatusActive {
		return fmt.Errorf("sync: cannot pause session %q with status %q", sessionID, sess.Status)
	}
	if s.mutagen == nil {
		return fmt.Errorf("sync: mutagen not available")
	}
	if sess.MutagenID == "" {
		return fmt.Errorf("sync: session %q has no mutagen session", sessionID)
	}
	if err := s.mutagen.PauseSession(ctx, sess.MutagenID); err != nil {
		return fmt.Errorf("sync: pause session %q: %w", sessionID, err)
	}
	sess.Status = domainsync.SyncStatusPaused
	if err := s.repo.Update(ctx, sess); err != nil {
		return err
	}
	if s.store != nil {
		if err := s.store.Update(sess); err != nil {
			return fmt.Errorf("sync: persist paused session: %w", err)
		}
	}
	return nil
}

// ResumeSync resumes a paused sync session.
func (s *Service) ResumeSync(ctx context.Context, sessionID string) error {
	sess, err := s.repo.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("sync: session %q not found", sessionID)
	}
	if sess.Status != domainsync.SyncStatusPaused {
		return fmt.Errorf("sync: session %q is not paused (status: %s)", sessionID, sess.Status)
	}
	if s.mutagen == nil {
		return fmt.Errorf("sync: mutagen not available")
	}
	if sess.MutagenID == "" {
		return fmt.Errorf("sync: session %q has no mutagen session", sessionID)
	}
	if err := s.mutagen.ResumeSession(ctx, sess.MutagenID); err != nil {
		return fmt.Errorf("sync: resume session %q: %w", sessionID, err)
	}
	sess.Status = domainsync.SyncStatusActive
	if err := s.repo.Update(ctx, sess); err != nil {
		return err
	}
	if s.store != nil {
		if err := s.store.Update(sess); err != nil {
			return fmt.Errorf("sync: persist resumed session: %w", err)
		}
	}
	return nil
}

// StartVolumeSync starts sync for a volume with explicit alpha/beta paths.
func (s *Service) StartVolumeSync(ctx context.Context, workspaceID, alphaPath, betaPath, direction string) (string, error) {
	dir := domainsync.SyncDirection(direction)
	mode, err := directionToMode(dir)
	if err != nil {
		return "", err
	}

	// Check if workspace already has an active sync.
	existing, _ := s.repo.GetByWorkspace(ctx, workspaceID)
	if existing != nil && existing.Status != domainsync.SyncStatusStopped {
		return "", fmt.Errorf("sync: workspace %q already has an active sync session %q", workspaceID, existing.ID)
	}

	alpha := alphaPath
	beta := betaPath
	if dir == domainsync.SyncDirectionDown {
		alpha, beta = beta, alpha
	}

	mutagenID := ""
	if s.mutagen != nil {
		mutagenID, err = s.mutagen.CreateSession(ctx, alpha, beta, mode)
		if err != nil {
			return "", fmt.Errorf("sync: create session: %w", err)
		}
	}

	id := mutagenID
	if id == "" {
		id = uuid.New().String()
	}

	sess := &domainsync.SyncSession{
		ID:          id,
		WorkspaceID: workspaceID,
		LocalPath:   alphaPath,
		Status:      domainsync.SyncStatusActive,
		Direction:   dir,
		Alpha:       alpha,
		Beta:        beta,
		MutagenID:   mutagenID,
		StartedAt:   time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, sess); err != nil {
		return "", fmt.Errorf("sync: store session: %w", err)
	}

	if s.store != nil {
		if err := s.store.Add(sess); err != nil {
			return "", fmt.Errorf("sync: persist session: %w", err)
		}
	}

	return id, nil
}
