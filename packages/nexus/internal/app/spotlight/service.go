package spotlight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// PortForwarder creates and manages spotlight port forwards.
// Implementations bind a local TCP port and proxy traffic to the remote port.
type PortForwarder interface {
	StartSpotlight(ctx context.Context, workspaceID string, spec spotlight.ExposeSpec) (*spotlight.Forward, error)
}

// PortDialer dials a connection into a workspace on a specific guest port.
// Used for libkrun VM workspaces to reach services via vsock, bypassing the
// bridge network and supporting loopback-only listeners inside the VM.
type PortDialer interface {
	DialPort(ctx context.Context, workspaceID string, remotePort int) (net.Conn, error)
}

// Service manages port forwards (spotlight) for workspaces.
var _ PortForwarder = (*Service)(nil)

type Service struct {
	repo          spotlight.Repository
	workspaceRepo workspace.Repository
	portDialer    PortDialer

	mu        sync.RWMutex
	listeners map[string]net.Listener
}

// Option configures spotlight service dependencies.
type Option func(*Service)

// WithPortDialer sets a PortDialer for workspace backends that need a
// daemon-side TCP proxy (libkrun VM). The daemon binds an ephemeral port and
// proxies each connection via the dialer so the CLI can SSH-forward to it.
func WithPortDialer(d PortDialer) Option {
	return func(s *Service) { s.portDialer = d }
}

// New creates a Service.
func New(repo spotlight.Repository, workspaceRepo workspace.Repository, opts ...Option) *Service {
	s := &Service{
		repo:          repo,
		workspaceRepo: workspaceRepo,
		listeners:     make(map[string]net.Listener),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// StartSpotlight creates and activates a port forward for the given workspace.
//
// Design:
//
//   - Non-VM workspaces: the service runs directly on the daemon host.
//     No TCP proxy is needed — we record the forward and return
//     targetHost=127.0.0.1 + targetPort=remotePort so the CLI can SSH-forward
//     directly to the service. Binding a proxy would collide with the service
//     itself (same port on the same host).
//
//   - libkrun VM workspaces: the service is inside a VM where loopback is not
//     reachable from outside. We bind an ephemeral TCP port on the daemon host
//     and proxy each connection via vsock (PortDialer) to the guest port. The
//     ephemeral port is returned as targetPort so the CLI SSH-tunnels to it.
//     Each workspace gets its own ephemeral port → no multi-tenant collision.
//
// Only one workspace may have active spotlight at a time. StartSpotlight stops
// any other workspace's forwards before creating new ones for workspaceID. This
// enforces the invariant at the daemon level regardless of how many clients call
// spotlight.start concurrently.
func (s *Service) StartSpotlight(ctx context.Context, workspaceID string, spec spotlight.ExposeSpec) (*spotlight.Forward, error) {
	if spec.LocalPort <= 0 || spec.RemotePort <= 0 {
		return nil, fmt.Errorf("localPort and remotePort must be > 0")
	}

	ws, err := s.workspaceRepo.Get(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}
	if ws == nil {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}

	// Enforce one-active-workspace invariant: stop spotlight on any other
	// workspace before adding forwards for this one. This prevents the Mac app
	// from seeing tunnels on multiple workspaces simultaneously and flickering.
	if err := s.stopOtherWorkspaces(ctx, workspaceID); err != nil {
		return nil, fmt.Errorf("stop other workspaces: %w", err)
	}

	// Deduplicate: if an existing forward for this workspace+localPort already
	// exists, close and remove it before creating a fresh one. This prevents
	// accumulation when the Mac app calls spotlight.start in bursts (e.g. on
	// reconnect) without calling spotlight.stop first.
	if err := s.closeExistingForward(ctx, workspaceID, spec.LocalPort); err != nil {
		return nil, fmt.Errorf("close existing forward: %w", err)
	}

	now := time.Now().UTC()
	id := fmt.Sprintf("spot-%d", now.UnixNano())

	fwd := &spotlight.Forward{
		ID:          id,
		WorkspaceID: workspaceID,
		LocalPort:   spec.LocalPort,
		RemotePort:  spec.RemotePort,
		Protocol:    spec.Protocol,
		State:       spotlight.ForwardStateActive,
		CreatedAt:   now,
	}
	if fwd.Protocol == "" {
		fwd.Protocol = "tcp"
	}
	fwd.TargetHost = "127.0.0.1"

	// libkrun VM workspaces: bind an ephemeral host-side TCP port and proxy
	// each connection via vsock into the VM. The guest agent's spotlight
	// listener (DefaultSpotlightVSockPort) dials 127.0.0.1:<remotePort> inside
	// the VM, so this works even for loopback-only services.
	if workspace.UsesGuestVM(ws.Backend) && s.portDialer != nil {
		guestPort := spec.RemotePort
		dialer := s.portDialer

		// Bind any free port on the daemon host — this is what the CLI will
		// SSH-forward to. Using :0 ensures each forward gets a unique port
		// and multiple workspaces sharing the same remotePort never collide.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("bind ephemeral port for spotlight: %w", err)
		}
		ephemeralPort := listener.Addr().(*net.TCPAddr).Port

		fwd.RemotePort = ephemeralPort // CLI will SSH-forward to this
		if err := s.repo.Create(ctx, fwd); err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("persist forward: %w", err)
		}

		s.mu.Lock()
		s.listeners[id] = listener
		s.mu.Unlock()

		go serveForwardWithDialer(listener, func(connCtx context.Context) (net.Conn, error) {
			return dialer.DialPort(connCtx, workspaceID, guestPort)
		})

		copy := *fwd
		return &copy, nil
	}

	// Non-VM workspaces: the service runs on the daemon host itself.
	// Return targetHost+targetPort so the CLI can SSH-forward directly to it.
	// No daemon-side proxy — binding the same port would collide with the
	// service and is redundant since SSH handles the forwarding.
	if err := s.repo.Create(ctx, fwd); err != nil {
		return nil, fmt.Errorf("persist forward: %w", err)
	}
	copy := *fwd
	return &copy, nil
}

// ListForwards returns all active forwards for a workspace.
func (s *Service) ListForwards(ctx context.Context, workspaceID string) ([]*spotlight.Forward, error) {
	fwds, err := s.repo.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list forwards: %w", err)
	}
	return fwds, nil
}

// CloseForward closes and removes a single forward by ID.
func (s *Service) CloseForward(ctx context.Context, forwardID string) error {
	if err := s.repo.Delete(ctx, forwardID); err != nil {
		return fmt.Errorf("delete forward: %w", err)
	}

	s.mu.Lock()
	if l, ok := s.listeners[forwardID]; ok {
		_ = l.Close()
		delete(s.listeners, forwardID)
	}
	s.mu.Unlock()

	return nil
}

// StopWorkspaceSpotlight closes all active forwards for the given workspace.
// This is the primary stop path — one spotlight per workspace is the design invariant.
func (s *Service) StopWorkspaceSpotlight(ctx context.Context, workspaceID string) error {
	fwds, err := s.repo.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("list forwards for workspace: %w", err)
	}
	for _, fwd := range fwds {
		if err := s.CloseForward(ctx, fwd.ID); err != nil && !errors.Is(err, spotlight.ErrNotFound) {
			return err
		}
	}
	return nil
}

// closeExistingForward closes and removes any existing forward for the given
// workspace+localPort pair. This prevents accumulation when StartSpotlight is
// called repeatedly (e.g. Mac app reconnect bursts) without a prior stop.
func (s *Service) closeExistingForward(ctx context.Context, workspaceID string, localPort int) error {
	fwds, err := s.repo.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("list forwards: %w", err)
	}
	for _, fwd := range fwds {
		if fwd.LocalPort == localPort {
			if err := s.CloseForward(ctx, fwd.ID); err != nil && !errors.Is(err, spotlight.ErrNotFound) {
				return err
			}
		}
	}
	return nil
}

// stopOtherWorkspaces stops spotlight on all workspaces except the given one.
// This enforces the one-active-workspace invariant at the daemon level.
func (s *Service) stopOtherWorkspaces(ctx context.Context, activeWorkspaceID string) error {
	workspaces, err := s.workspaceRepo.List(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}
	for _, ws := range workspaces {
		if ws.ID == activeWorkspaceID {
			continue
		}
		fwds, err := s.repo.ListByWorkspace(ctx, ws.ID)
		if err != nil {
			return fmt.Errorf("list forwards for workspace %s: %w", ws.ID, err)
		}
		if len(fwds) == 0 {
			continue
		}
		log.Printf("[spotlight] stopping spotlight for workspace %s (switching to %s)", ws.ID, activeWorkspaceID)
		if err := s.StopWorkspaceSpotlight(ctx, ws.ID); err != nil {
			return fmt.Errorf("stop spotlight for workspace %s: %w", ws.ID, err)
		}
	}
	return nil
}

// serveForwardWithDialer accepts TCP connections on listener and proxies each
// via a fresh connection returned by dial. Used for libkrun vsock proxying.
func serveForwardWithDialer(listener net.Listener, dial func(ctx context.Context) (net.Conn, error)) {
	for {
		clientConn, err := listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			upstream, err := dial(ctx)
			cancel()
			if err != nil {
				_ = c.Close()
				return
			}
			proxyConns(c, upstream)
		}(clientConn)
	}
}

func proxyConns(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	<-done
	_ = a.Close()
	_ = b.Close()
}
