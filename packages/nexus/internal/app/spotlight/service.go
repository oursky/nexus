package spotlight

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
)

// PortForwarder creates and manages spotlight port forwards.
// Implementations bind a local TCP port and proxy traffic to the remote port.
type PortForwarder interface {
	StartSpotlight(ctx context.Context, workspaceID string, spec spotlight.ExposeSpec) (*spotlight.Forward, error)
}

// EndpointResolver resolves how to reach a workspace-internal service from the daemon host.
type EndpointResolver interface {
	ResolveWorkspaceEndpoint(ctx context.Context, workspaceID string, remotePort int) (host string, port int, err error)
}

// Service manages port forwards (spotlight) for workspaces.
var _ PortForwarder = (*Service)(nil)

type Service struct {
	repo          spotlight.Repository
	workspaceRepo workspace.Repository
	resolver      EndpointResolver

	mu        sync.RWMutex
	listeners map[string]net.Listener
}

// Option configures spotlight service dependencies.
type Option func(*Service)

// WithEndpointResolver enables internal workspace endpoint routing without daemon-host binds.
func WithEndpointResolver(r EndpointResolver) Option {
	return func(s *Service) { s.resolver = r }
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

	// Firecracker workspaces should route spotlight directly to guest-internal
	// service endpoints instead of binding daemon-host ports.
	if ws.Backend == "firecracker" && s.resolver != nil {
		targetHost, targetPort, err := s.resolver.ResolveWorkspaceEndpoint(ctx, workspaceID, spec.RemotePort)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace endpoint: %w", err)
		}
		if targetHost == "" || targetPort <= 0 {
			return nil, fmt.Errorf("invalid workspace endpoint %q:%d", targetHost, targetPort)
		}
		fwd.TargetHost = targetHost
		fwd.RemotePort = targetPort
		if err := s.repo.Create(ctx, fwd); err != nil {
			return nil, fmt.Errorf("persist forward: %w", err)
		}
		copy := *fwd
		return &copy, nil
	}

	host := "127.0.0.1"
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, spec.LocalPort))
	if err != nil {
		return nil, fmt.Errorf("bind local port %d: %w", spec.LocalPort, err)
	}

	if err := s.repo.Create(ctx, fwd); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("persist forward: %w", err)
	}

	s.mu.Lock()
	s.listeners[id] = listener
	s.mu.Unlock()

	targetAddr := fmt.Sprintf("%s:%d", host, spec.RemotePort)
	go serveForward(listener, targetAddr)

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

// CloseForward closes and removes a forward by ID.
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

func serveForward(listener net.Listener, targetAddr string) {
	for {
		clientConn, err := listener.Accept()
		if err != nil {
			return
		}
		go proxyTCP(clientConn, targetAddr)
	}
}

func proxyTCP(clientConn net.Conn, targetAddr string) {
	upstreamConn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		_ = clientConn.Close()
		return
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstreamConn, clientConn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, upstreamConn)
		done <- struct{}{}
	}()
	<-done
	_ = clientConn.Close()
	_ = upstreamConn.Close()
}
