// Package sync provides volume synchronization via embedded mutagen.
package sync

import (
	"context"
	"fmt"
	"io"

	"github.com/mutagen-io/mutagen/pkg/logging"
	"github.com/mutagen-io/mutagen/pkg/selection"
	"github.com/mutagen-io/mutagen/pkg/synchronization"
	"github.com/mutagen-io/mutagen/pkg/synchronization/core"
	_ "github.com/mutagen-io/mutagen/pkg/synchronization/protocols/local"
	_ "github.com/mutagen-io/mutagen/pkg/synchronization/protocols/ssh"
	"github.com/mutagen-io/mutagen/pkg/url"
)

// EmbeddedMutagenClient wraps mutagen's synchronization.Manager for in-process use.
// This eliminates the need for a separate mutagen daemon process.
type EmbeddedMutagenClient struct {
	manager *synchronization.Manager
}

// NewEmbeddedMutagenClient creates a new EmbeddedMutagenClient.
func NewEmbeddedMutagenClient() (*EmbeddedMutagenClient, error) {
	logger := logging.NewLogger(logging.LevelWarn, io.Discard)
	manager, err := synchronization.NewManager(logger)
	if err != nil {
		return nil, fmt.Errorf("sync: failed to create mutagen manager: %w", err)
	}
	return &EmbeddedMutagenClient{manager: manager}, nil
}

// mutagenMode maps a SyncDirection to the corresponding mutagen SynchronizationMode.
func embeddedMutagenMode(dir SyncDirection) core.SynchronizationMode {
	switch dir {
	case SyncDirectionBidirectional:
		return core.SynchronizationMode_SynchronizationModeTwoWaySafe
	case SyncDirectionUp, SyncDirectionDown:
		// One-way replica: alpha → beta when Up; caller should swap alpha/beta for Down.
		return core.SynchronizationMode_SynchronizationModeOneWayReplica
	default:
		return core.SynchronizationMode_SynchronizationModeTwoWaySafe
	}
}

// CreateSession creates a new mutagen synchronization session.
func (c *EmbeddedMutagenClient) CreateSession(ctx context.Context, alpha, beta string, mode SyncDirection) (string, error) {
	alphaURL, err := url.Parse(alpha, url.Kind_Synchronization, true)
	if err != nil {
		return "", fmt.Errorf("sync: invalid alpha URL %q: %w", alpha, err)
	}

	betaURL, err := url.Parse(beta, url.Kind_Synchronization, false)
	if err != nil {
		return "", fmt.Errorf("sync: invalid beta URL %q: %w", beta, err)
	}

	cfg := &synchronization.Configuration{
		SynchronizationMode: embeddedMutagenMode(mode),
	}

	// Provide empty (but non-nil) endpoint configurations to avoid nil pointer issues.
	cfgAlpha := &synchronization.Configuration{}
	cfgBeta := &synchronization.Configuration{}

	sessionID, err := c.manager.Create(ctx, alphaURL, betaURL, cfg, cfgAlpha, cfgBeta, "", nil, false, "")
	if err != nil {
		return "", fmt.Errorf("sync: create session failed: %w", err)
	}

	return sessionID, nil
}

// TerminateSession terminates the mutagen session identified by sessionID.
func (c *EmbeddedMutagenClient) TerminateSession(ctx context.Context, sessionID string) error {
	sel := &selection.Selection{
		Specifications: []string{sessionID},
	}
	if err := c.manager.Terminate(ctx, sel, ""); err != nil {
		return fmt.Errorf("sync: terminate session %q failed: %w", sessionID, err)
	}
	return nil
}

// PauseSession pauses the mutagen session identified by sessionID.
func (c *EmbeddedMutagenClient) PauseSession(ctx context.Context, sessionID string) error {
	sel := &selection.Selection{
		Specifications: []string{sessionID},
	}
	if err := c.manager.Pause(ctx, sel, ""); err != nil {
		return fmt.Errorf("sync: pause session %q failed: %w", sessionID, err)
	}
	return nil
}

// ResumeSession resumes the mutagen session identified by sessionID.
func (c *EmbeddedMutagenClient) ResumeSession(ctx context.Context, sessionID string) error {
	sel := &selection.Selection{
		Specifications: []string{sessionID},
	}
	if err := c.manager.Resume(ctx, sel, ""); err != nil {
		return fmt.Errorf("sync: resume session %q failed: %w", sessionID, err)
	}
	return nil
}

// SessionStatus returns a human-readable status string for the given session.
func (c *EmbeddedMutagenClient) SessionStatus(ctx context.Context, sessionID string) (string, error) {
	sel := &selection.Selection{
		Specifications: []string{sessionID},
	}
	_, states, err := c.manager.List(ctx, sel, 0)
	if err != nil {
		return "", fmt.Errorf("sync: list sessions failed: %w", err)
	}

	for _, state := range states {
		if state.Session.Identifier == sessionID {
			return state.Status.String(), nil
		}
	}

	return "", fmt.Errorf("sync: session %q not found", sessionID)
}
