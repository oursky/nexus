package workspace

import (
	"context"
	"sort"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/config"
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

	merged := make(map[int]workspace.DiscoveredPort)

	// Load Nexusfile ports first so they take precedence.
	if cfg, _, _ := config.LoadNexusfile(ws.Repo); true {
		if cfg.Dev.Port > 0 {
			merged[cfg.Dev.Port] = workspace.DiscoveredPort{
				LocalPort:  cfg.Dev.Port,
				RemotePort: cfg.Dev.Port,
				Source:     "config",
			}
		}
		for _, p := range cfg.Dev.Ports {
			if p.Port <= 0 {
				continue
			}
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			merged[p.Port] = workspace.DiscoveredPort{
				LocalPort:  p.Port,
				RemotePort: p.Port,
				Service:    p.Service,
				Protocol:   proto,
				Source:     "config",
			}
		}
	}

	// Discover docker-compose ports.
	// HostPort is the port published on the daemon host (e.g., 8080 for "8080:80").
	// Both LocalPort and RemotePort use HostPort since we tunnel to the daemon's published port.
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if s.portDiscoverer != nil {
		if ports, err := s.portDiscoverer.DiscoverPublishedPorts(discoverCtx, ws.Repo); err == nil {
			for _, p := range ports {
				if _, exists := merged[p.RemotePort]; !exists {
					merged[p.RemotePort] = workspace.DiscoveredPort{
						LocalPort:  p.RemotePort,
						RemotePort: p.RemotePort,
						Service:    p.Service,
						Protocol:   p.Protocol,
						Source:     "compose",
					}
				}
			}
		}
	}

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
