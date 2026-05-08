// Package sync provides volume synchronization via mutagen.
package sync

import (
	"context"
	"fmt"

	"github.com/mutagen-io/mutagen/pkg/selection"
	"github.com/mutagen-io/mutagen/pkg/service/synchronization"
	mutagensync "github.com/mutagen-io/mutagen/pkg/synchronization"
	"github.com/mutagen-io/mutagen/pkg/synchronization/core"
	"github.com/mutagen-io/mutagen/pkg/url"
	"google.golang.org/grpc"
)

// SyncDirection specifies the direction of file synchronization.
type SyncDirection int

const (
	// SyncDirectionBidirectional syncs changes in both directions (two-way safe).
	SyncDirectionBidirectional SyncDirection = iota
	// SyncDirectionUp syncs from alpha (local) to beta (remote).
	SyncDirectionUp
	// SyncDirectionDown syncs from beta (remote) to alpha (local).
	SyncDirectionDown
)

// MutagenClient wraps mutagen's session management for volume synchronization.
type MutagenClient struct {
	conn   *grpc.ClientConn
	client synchronization.SynchronizationClient
}

// NewMutagenClient creates a MutagenClient using the provided gRPC connection.
// The caller is responsible for managing the connection lifecycle.
func NewMutagenClient(conn *grpc.ClientConn) *MutagenClient {
	return &MutagenClient{
		conn:   conn,
		client: synchronization.NewSynchronizationClient(conn),
	}
}

// mutagenMode maps a SyncDirection to the corresponding mutagen SynchronizationMode.
func mutagenMode(dir SyncDirection) core.SynchronizationMode {
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
//
// alpha and beta are mutagen URL strings (e.g. "/local/path" or "user@host:/remote/path").
// For SyncDirectionDown the caller should swap alpha and beta so that the
// remote side is always treated as the authoritative replica source.
//
// Returns the new session identifier on success.
func (c *MutagenClient) CreateSession(ctx context.Context, alpha, beta string, mode SyncDirection) (string, error) {
	alphaURL, err := url.Parse(alpha, url.Kind_Synchronization, true)
	if err != nil {
		return "", fmt.Errorf("sync: invalid alpha URL %q: %w", alpha, err)
	}

	betaURL, err := url.Parse(beta, url.Kind_Synchronization, false)
	if err != nil {
		return "", fmt.Errorf("sync: invalid beta URL %q: %w", beta, err)
	}

	cfg := &mutagensync.Configuration{
		SynchronizationMode: mutagenMode(mode),
	}

	req := &synchronization.CreateRequest{
		Specification: &synchronization.CreationSpecification{
			Alpha:         alphaURL,
			Beta:          betaURL,
			Configuration: cfg,
		},
	}

	resp, err := c.client.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("sync: create session failed: %w", err)
	}

	return resp.GetSession(), nil
}

// TerminateSession terminates the mutagen session identified by sessionID.
func (c *MutagenClient) TerminateSession(ctx context.Context, sessionID string) error {
	req := &synchronization.TerminateRequest{
		Selection: &selection.Selection{
			Specifications: []string{sessionID},
		},
	}

	if _, err := c.client.Terminate(ctx, req); err != nil {
		return fmt.Errorf("sync: terminate session %q failed: %w", sessionID, err)
	}

	return nil
}

// SessionStatus returns a human-readable status string for the given session.
func (c *MutagenClient) SessionStatus(ctx context.Context, sessionID string) (string, error) {
	req := &synchronization.ListRequest{
		Selection: &selection.Selection{
			Specifications: []string{sessionID},
		},
	}

	resp, err := c.client.List(ctx, req)
	if err != nil {
		return "", fmt.Errorf("sync: list session %q failed: %w", sessionID, err)
	}

	states := resp.GetSessionStates()
	if len(states) == 0 {
		return "", fmt.Errorf("sync: session %q not found", sessionID)
	}

	state := states[0]
	status := state.GetStatus().String()

	return status, nil
}
