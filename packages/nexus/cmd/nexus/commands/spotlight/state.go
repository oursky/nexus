package spotlight

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
)

type spotlightClientState struct {
	Profiles map[string]spotlightProfileState `json:"profiles"`
}

type spotlightProfileState struct {
	WorkspaceID string `json:"workspaceId"`
	// TunnelPID is the PID of the single SSH process managing all forwards.
	TunnelPID int `json:"tunnelPid,omitempty"`
}

// loadTunnelPIDsForWorkspace returns the active tunnel PIDs associated with the
// workspace across all stored profile entries.
func loadTunnelPIDsForWorkspace(workspaceID string) []int {
	state, err := loadSpotlightClientState()
	if err != nil {
		return nil
	}
	seen := map[int]struct{}{}
	var pids []int
	for _, entry := range state.Profiles {
		if entry.WorkspaceID != workspaceID {
			continue
		}
		if entry.TunnelPID <= 0 {
			continue
		}
		if _, ok := seen[entry.TunnelPID]; ok {
			continue
		}
		seen[entry.TunnelPID] = struct{}{}
		pids = append(pids, entry.TunnelPID)
	}
	return pids
}

func spotlightStatePath() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdg != "" {
		return filepath.Join(xdg, "nexus", "spotlight-client-state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "nexus", "spotlight-client-state.json"), nil
}

func spotlightProfileKey(p *profile.Profile) string {
	return fmt.Sprintf("%s|%d|%d", p.Host, p.Port, p.SSHPort)
}

func loadSpotlightClientState() (*spotlightClientState, error) {
	path, err := spotlightStatePath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &spotlightClientState{Profiles: map[string]spotlightProfileState{}}, nil
		}
		return nil, fmt.Errorf("read spotlight state: %w", err)
	}
	var state spotlightClientState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode spotlight state: %w", err)
	}
	if state.Profiles == nil {
		state.Profiles = map[string]spotlightProfileState{}
	}
	return &state, nil
}

func saveSpotlightClientState(state *spotlightClientState) error {
	path, err := spotlightStatePath()
	if err != nil {
		return err
	}
	if state == nil {
		state = &spotlightClientState{Profiles: map[string]spotlightProfileState{}}
	}
	if state.Profiles == nil {
		state.Profiles = map[string]spotlightProfileState{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir spotlight state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode spotlight state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write spotlight state: %w", err)
	}
	return nil
}
