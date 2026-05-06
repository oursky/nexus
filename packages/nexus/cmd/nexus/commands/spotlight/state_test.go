package spotlight

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTunnelPIDsForWorkspace_ReadsSingleTunnelPID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	path, err := spotlightStatePath()
	if err != nil {
		t.Fatalf("spotlightStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	state := `{
	  "profiles": {
	    "default": {
	      "workspaceId": "ws-1",
	      "tunnelPid": 303
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pids := loadTunnelPIDsForWorkspace("ws-1")
	if len(pids) != 1 || pids[0] != 303 {
		t.Fatalf("loadTunnelPIDsForWorkspace = %v, want [303]", pids)
	}
}

func TestLoadTunnelPIDsForWorkspace_DedupesProfiles(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	path, err := spotlightStatePath()
	if err != nil {
		t.Fatalf("spotlightStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	state := `{
	  "profiles": {
	    "default": {
	      "workspaceId": "ws-1",
	      "tunnelPid": 303
	    },
	    "other": {
	      "workspaceId": "ws-1",
	      "tunnelPid": 303
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pids := loadTunnelPIDsForWorkspace("ws-1")
	if len(pids) != 1 || pids[0] != 303 {
		t.Fatalf("loadTunnelPIDsForWorkspace = %v, want [303]", pids)
	}
}
