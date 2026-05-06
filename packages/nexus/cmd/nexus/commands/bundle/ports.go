package bundle

import (
	"github.com/oursky/nexus/packages/nexus/internal/infra/dockercompose"
)

// discoverBundlePorts scans the workspace directory for docker-compose files
// and returns the unique host ports that should be forwarded from localhost
// to the VM guest.
func discoverBundlePorts(workspaceDir string) []int {
	ports, err := dockercompose.DiscoverPublishedPortsFromYAML(workspaceDir)
	if err != nil {
		return nil
	}
	seen := make(map[int]bool)
	var result []int
	for _, p := range ports {
		if seen[p.HostPort] {
			continue
		}
		seen[p.HostPort] = true
		result = append(result, p.HostPort)
	}
	return result
}
