package dockercompose

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DiscoverPublishedPortsFromYAML parses docker-compose files directly
// without requiring the docker binary. It searches for compose files in
// workspaceRoot and extracts published port mappings from the services
// ports section.
func DiscoverPublishedPortsFromYAML(workspaceRoot string) ([]PublishedPort, error) {
	composeFiles, found, err := findComposeFiles(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrComposeFileNotFound
	}

	// Merge ports from all compose files (later files override earlier on conflict).
	seen := make(map[string]bool)
	var allPorts []PublishedPort
	for _, f := range composeFiles {
		ports, err := parsePublishedPortsFromYAMLFile(f)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", filepath.Base(f), err)
		}
		for _, p := range ports {
			key := fmt.Sprintf("%s/%d/%s", p.Service, p.HostPort, p.Protocol)
			if seen[key] {
				continue
			}
			seen[key] = true
			allPorts = append(allPorts, p)
		}
	}

	sort.Slice(allPorts, func(i, j int) bool {
		if allPorts[i].Service != allPorts[j].Service {
			return allPorts[i].Service < allPorts[j].Service
		}
		if allPorts[i].HostPort != allPorts[j].HostPort {
			return allPorts[i].HostPort < allPorts[j].HostPort
		}
		return allPorts[i].TargetPort < allPorts[j].TargetPort
	})

	return allPorts, nil
}

func parsePublishedPortsFromYAMLFile(path string) ([]PublishedPort, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc yamlComposeDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}

	var result []PublishedPort
	for serviceName, svc := range doc.Services {
		for _, raw := range svc.Ports {
			p, ok := parseYAMLPortMapping(serviceName, raw)
			if !ok {
				continue
			}
			result = append(result, p)
		}
	}
	return result, nil
}

type yamlComposeDoc struct {
	Services map[string]yamlComposeService `yaml:"services"`
}

type yamlComposeService struct {
	Ports []yamlPortEntry `yaml:"ports"`
}

// yamlPortEntry is a custom type that handles both string and object ports.
type yamlPortEntry struct {
	Raw *yaml.Node
}

func (e *yamlPortEntry) UnmarshalYAML(node *yaml.Node) error {
	e.Raw = node
	return nil
}

func parseYAMLPortMapping(service string, entry yamlPortEntry) (PublishedPort, bool) {
	if entry.Raw == nil {
		return PublishedPort{}, false
	}

	switch entry.Raw.Kind {
	case yaml.ScalarNode:
		return parseYAMLStringPortMapping(service, entry.Raw.Value)
	case yaml.MappingNode:
		return parseYAMLObjectPortMapping(service, entry.Raw)
	default:
		return PublishedPort{}, false
	}
}

func parseYAMLObjectPortMapping(service string, node *yaml.Node) (PublishedPort, bool) {
	var obj map[string]yaml.Node
	if err := node.Decode(&obj); err != nil || len(obj) == 0 {
		return PublishedPort{}, false
	}

	published, ok := yamlNodeAsInt(getYAMLNodePtr(obj, "published"))
	if !ok || published <= 0 {
		return PublishedPort{}, false
	}

	target, ok := yamlNodeAsInt(getYAMLNodePtr(obj, "target"))
	if !ok || target <= 0 {
		return PublishedPort{}, false
	}

	protocol := yamlNodeAsString(getYAMLNodePtr(obj, "protocol"))
	if protocol == "" {
		protocol = "tcp"
	}

	hostIP := yamlNodeAsString(getYAMLNodePtr(obj, "host_ip"))
	if hostIP == "" {
		hostIP = yamlNodeAsString(getYAMLNodePtr(obj, "hostIP"))
	}

	return PublishedPort{
		Service:    service,
		HostIP:     hostIP,
		HostPort:   published,
		TargetPort: target,
		Protocol:   protocol,
	}, true
}

func getYAMLNodePtr(m map[string]yaml.Node, key string) *yaml.Node {
	if n, ok := m[key]; ok {
		return &n
	}
	return nil
}

func parseYAMLStringPortMapping(service, spec string) (PublishedPort, bool) {
	if spec == "" {
		return PublishedPort{}, false
	}

	protocol := "tcp"
	if strings.Contains(spec, "/") {
		parts := strings.SplitN(spec, "/", 2)
		spec = parts[0]
		if parts[1] != "" {
			protocol = parts[1]
		}
	}

	parts := strings.Split(spec, ":")
	var hostIP string
	var publishedStr string
	var targetStr string

	switch len(parts) {
	case 1:
		// "3000" — exposed port only (no host mapping)
		return PublishedPort{}, false
	case 2:
		publishedStr = parts[0]
		targetStr = parts[1]
	case 3:
		hostIP = parts[0]
		publishedStr = parts[1]
		targetStr = parts[2]
	default:
		return PublishedPort{}, false
	}

	published, err := strconv.Atoi(publishedStr)
	if err != nil || published <= 0 {
		return PublishedPort{}, false
	}
	target, err := strconv.Atoi(targetStr)
	if err != nil || target <= 0 {
		return PublishedPort{}, false
	}

	return PublishedPort{
		Service:    service,
		HostIP:     hostIP,
		HostPort:   published,
		TargetPort: target,
		Protocol:   protocol,
	}, true
}

func yamlNodeAsInt(node *yaml.Node) (int, bool) {
	if node == nil {
		return 0, false
	}
	switch node.Tag {
	case "!!int":
		i, err := strconv.Atoi(node.Value)
		if err != nil {
			return 0, false
		}
		return i, true
	case "!!str":
		i, err := strconv.Atoi(node.Value)
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func yamlNodeAsString(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	return node.Value
}
