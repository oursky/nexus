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
	// TunnelPID is the PID of the single SSH process managing all forwards (new format).
	TunnelPID int `json:"tunnelPid,omitempty"`
	// TunnelPIDs is the legacy multi-PID format (one SSH per port). Read-only for migration.
	TunnelPIDs []int `json:"tunnelPids,omitempty"`
}

// allTunnelPIDs returns all PIDs to kill for this entry, handling both old and new formats.
func (s spotlightProfileState) allTunnelPIDs() []int {
	if s.TunnelPID > 0 {
		return []int{s.TunnelPID}
	}
	return s.TunnelPIDs
}

// loadTunnelPIDForWorkspace returns the SSH tunnel PID persisted for the given workspaceID.
func loadTunnelPIDForWorkspace(workspaceID string) (int, bool) {
	p, err := profile.LoadDefault()
	if err != nil {
		return 0, false
	}
	state, err := loadSpotlightClientState()
	if err != nil {
		return 0, false
	}
	key := spotlightProfileKey(p)
	if entry, ok := state.Profiles[key]; ok && entry.WorkspaceID == workspaceID {
		return entry.TunnelPID, true
	}
	return 0, false
}

// loadTunnelPIDsForWorkspace returns all tunnel PIDs associated with the
// workspace across all stored profile entries. This handles both the new
// single-PID and legacy multi-PID formats.
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
		for _, pid := range entry.allTunnelPIDs() {
			if pid <= 0 {
				continue
			}
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			pids = append(pids, pid)
		}
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
