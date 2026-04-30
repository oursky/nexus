package dockercompose

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrComposeFileNotFound    = errors.New("docker compose file not found")
	ErrComposeJSONUnsupported = errors.New("docker compose json output unsupported")
)

type composeCommandOutput struct {
	stdout []byte
	stderr []byte
}

type PublishedPort struct {
	Service    string `json:"service"`
	HostIP     string `json:"hostIP,omitempty"`
	HostPort   int    `json:"hostPort"`
	TargetPort int    `json:"targetPort"`
	Protocol   string `json:"protocol"`
}

var runComposeCommand = func(ctx context.Context, workspaceRoot string, args ...string) (composeCommandOutput, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Dir = workspaceRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	return composeCommandOutput{stdout: stdout, stderr: stderr.Bytes()}, err
}

func DiscoverPublishedPorts(ctx context.Context, workspaceRoot string) ([]PublishedPort, error) {
	composeFiles, found, err := findComposeFiles(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrComposeFileNotFound
	}

	// Build "-f file1 -f file2 ..." args followed by the subcommand.
	var fileArgs []string
	for _, f := range composeFiles {
		fileArgs = append(fileArgs, "-f", f)
	}
	configArgs := append(fileArgs, "config", "--format", "json")
	out, err := runComposeCommand(ctx, workspaceRoot, configArgs...)
	if err != nil {
		_, _ = runComposeCommand(ctx, workspaceRoot, append(fileArgs, "config")...)
		return nil, fmt.Errorf("%w: %v", ErrComposeJSONUnsupported, err)
	}

	trimmed := bytes.TrimSpace(out.stdout)
	if !json.Valid(trimmed) {
		snippet := strings.TrimSpace(string(trimmed))
		if snippet == "" {
			snippet = strings.TrimSpace(string(out.stderr))
		}
		if snippet == "" {
			snippet = "empty output"
		}
		if len(snippet) > 160 {
			snippet = snippet[:160]
		}
		return nil, fmt.Errorf("%w: non-json output from docker compose config --format json: %s", ErrComposeJSONUnsupported, snippet)
	}

	ports, err := parsePublishedPortsFromConfigJSON(trimmed)
	if err != nil {
		return nil, err
	}

	return ports, nil
}

// findComposeFiles returns the ordered list of docker-compose files to pass to
// "docker compose -f" for the given workspace root. It searches:
//
//  1. The workspace root itself (docker-compose.yml / docker-compose.yaml).
//  2. One level of subdirectories (e.g. backend/docker-compose.yaml).
//
// For each primary compose file found it also appends the sibling override
// file (docker-compose.override.yml / docker-compose.override.yaml) when
// present. The first directory that contains a primary compose file wins;
// subdirectory search stops there.
//
// Returns (files, found, error). files is nil when not found.
func findComposeFiles(workspaceRoot string) ([]string, bool, error) {
	primaryNames := []string{"docker-compose.yml", "docker-compose.yaml"}
	overrideNames := []string{"docker-compose.override.yml", "docker-compose.override.yaml"}

	findInDir := func(dir string) ([]string, bool, error) {
		var primary string
		for _, name := range primaryNames {
			p := filepath.Join(dir, name)
			info, err := os.Stat(p)
			if err == nil && !info.IsDir() {
				primary = p
				break
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, false, err
			}
		}
		if primary == "" {
			return nil, false, nil
		}
		files := []string{primary}
		for _, name := range overrideNames {
			p := filepath.Join(dir, name)
			info, err := os.Stat(p)
			if err == nil && !info.IsDir() {
				files = append(files, p)
				break
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, false, err
			}
		}
		return files, true, nil
	}

	// 1. Check workspace root first.
	if files, found, err := findInDir(workspaceRoot); err != nil || found {
		return files, found, err
	}

	// 2. Search one level of subdirectories.
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil, false, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subdir := filepath.Join(workspaceRoot, e.Name())
		if files, found, err := findInDir(subdir); err != nil || found {
			return files, found, err
		}
	}

	return nil, false, nil
}

func parsePublishedPortsFromConfigJSON(data []byte) ([]PublishedPort, error) {
	type composeService struct {
		Ports []json.RawMessage `json:"ports"`
	}
	type composeConfig struct {
		Services map[string]composeService `json:"services"`
	}

	var cfg composeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse compose config json: %w", err)
	}

	result := make([]PublishedPort, 0)
	for service, svc := range cfg.Services {
		for _, raw := range svc.Ports {
			if p, ok := parseObjectPortMapping(service, raw); ok {
				result = append(result, p)
				continue
			}
			if p, ok := parseStringPortMapping(service, raw); ok {
				result = append(result, p)
			}
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Service != result[j].Service {
			return result[i].Service < result[j].Service
		}
		if result[i].HostPort != result[j].HostPort {
			return result[i].HostPort < result[j].HostPort
		}
		return result[i].TargetPort < result[j].TargetPort
	})

	return result, nil
}

func parseObjectPortMapping(service string, raw json.RawMessage) (PublishedPort, bool) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
		return PublishedPort{}, false
	}

	published, ok := asInt(obj["published"])
	if !ok || published <= 0 {
		return PublishedPort{}, false
	}

	target, ok := asInt(obj["target"])
	if !ok || target <= 0 {
		return PublishedPort{}, false
	}

	protocol := asString(obj["protocol"])
	if protocol == "" {
		protocol = "tcp"
	}

	hostIP := asString(obj["host_ip"])
	if hostIP == "" {
		hostIP = asString(obj["hostIP"])
	}

	return PublishedPort{
		Service:    service,
		HostIP:     hostIP,
		HostPort:   published,
		TargetPort: target,
		Protocol:   protocol,
	}, true
}

func parseStringPortMapping(service string, raw json.RawMessage) (PublishedPort, bool) {
	var spec string
	if err := json.Unmarshal(raw, &spec); err != nil || spec == "" {
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

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
