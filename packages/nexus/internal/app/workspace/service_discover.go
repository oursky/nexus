package workspace

import (
	"context"
	"sort"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// DiscoverPorts merges config defaults with docker-compose port discovery.
// Config defaults take precedence on conflict (same remote port).
func (s *Service) DiscoverPorts(ctx context.Context, id string) ([]workspace.DiscoveredPort, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.Repo == "" {
		return nil, nil
	}

	// Discover docker-compose ports.
	// HostPort is the port published on the daemon host (e.g., 8080 for "8080:80").
	// Both LocalPort and RemotePort use HostPort since we tunnel to the daemon's published port.
	composeDefaults := make(map[int]workspace.DiscoveredPort) // HostPort → port
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if s.portDiscoverer != nil {
		if ports, err := s.portDiscoverer.DiscoverPublishedPorts(discoverCtx, ws.Repo); err == nil {
			for _, p := range ports {
				composeDefaults[p.RemotePort] = workspace.DiscoveredPort{
					LocalPort:  p.RemotePort,
					RemotePort: p.RemotePort,
					Service:    p.Service,
					Protocol:   p.Protocol,
					Source:     "compose",
				}
			}
		}
	}

	merged := composeDefaults

	// Build sorted result
	result := make([]workspace.DiscoveredPort, 0, len(merged))
	for _, p := range merged {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].RemotePort < result[j].RemotePort
	})
	return result, nil
}
