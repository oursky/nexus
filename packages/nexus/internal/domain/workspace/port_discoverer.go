package workspace

import "context"

// DiscoveredPort represents a port exposed by a workspace.
type DiscoveredPort struct {
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
	Service    string `json:"service,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Source     string `json:"source,omitempty"` // "config" or "compose"
}

// PortDiscoverer discovers published ports for a workspace repository.
type PortDiscoverer interface {
	DiscoverPublishedPorts(ctx context.Context, repoRoot string) ([]DiscoveredPort, error)
}
