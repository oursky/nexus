// Package process implements a pkg/runtime.Driver backed by the host process
// sandbox (macOS sandbox-exec / Linux bubblewrap). It replaces the old
// Lima-based "seatbelt" driver with true on-host process isolation.
package process

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/creack/pty"
	"github.com/inizio/nexus/packages/nexus/internal/infra/runtime/sandbox"
	"github.com/inizio/nexus/packages/nexus/pkg/runtime"
)

// Driver implements runtime.Driver for the process sandbox backend.
type Driver struct {
	mu         sync.RWMutex
	workspaces map[string]*workspaceState
}

type workspaceState struct {
	projectRoot string
	state       string
}

var _ runtime.Driver = (*Driver)(nil)

// NewDriver creates a new process sandbox runtime driver.
func NewDriver() *Driver {
	return &Driver{
		workspaces: make(map[string]*workspaceState),
	}
}

// Backend returns "process".
func (d *Driver) Backend() string { return "process" }

func (d *Driver) Create(_ context.Context, req runtime.CreateRequest) error {
	if req.WorkspaceID == "" {
		return fmt.Errorf("workspace id is required")
	}
	if req.ProjectRoot == "" {
		return fmt.Errorf("project root is required")
	}
	if _, err := os.Stat(req.ProjectRoot); err != nil {
		return fmt.Errorf("project root not accessible: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.workspaces[req.WorkspaceID]; exists {
		return fmt.Errorf("workspace %s already exists", req.WorkspaceID)
	}
	d.workspaces[req.WorkspaceID] = &workspaceState{
		projectRoot: req.ProjectRoot,
		state:       "created",
	}
	return nil
}

func (d *Driver) Start(_ context.Context, workspaceID string) error {
	return d.setState(workspaceID, "running")
}

func (d *Driver) Stop(_ context.Context, workspaceID string) error {
	return d.setState(workspaceID, "stopped")
}

func (d *Driver) Restore(_ context.Context, workspaceID string) error {
	return d.setState(workspaceID, "running")
}

func (d *Driver) Pause(_ context.Context, workspaceID string) error {
	return d.setState(workspaceID, "paused")
}

func (d *Driver) Resume(_ context.Context, workspaceID string) error {
	return d.setState(workspaceID, "running")
}

func (d *Driver) Fork(_ context.Context, workspaceID, childWorkspaceID string) error {
	d.mu.Lock()
	parent, ok := d.workspaces[workspaceID]
	if !ok {
		d.mu.Unlock()
		return fmt.Errorf("workspace %s not found", workspaceID)
	}
	if _, exists := d.workspaces[childWorkspaceID]; exists {
		d.mu.Unlock()
		return fmt.Errorf("workspace %s already exists", childWorkspaceID)
	}
	parentRoot := parent.projectRoot
	d.mu.Unlock()

	childRoot := parentRoot + "-fork-" + childWorkspaceID
	if err := sandbox.ForkWorktree(parentRoot, childRoot); err != nil {
		return fmt.Errorf("fork workspace %s -> %s: %w", workspaceID, childWorkspaceID, err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.workspaces[childWorkspaceID] = &workspaceState{
		projectRoot: childRoot,
		state:       "created",
	}
	return nil
}

func (d *Driver) Destroy(_ context.Context, workspaceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.workspaces[workspaceID]; !ok {
		return fmt.Errorf("workspace %s not found", workspaceID)
	}
	delete(d.workspaces, workspaceID)
	return nil
}

func (d *Driver) setState(workspaceID, state string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ws, ok := d.workspaces[workspaceID]
	if !ok {
		// Tolerate unknown workspaces for state transitions (recovery path).
		d.workspaces[workspaceID] = &workspaceState{state: state}
		return nil
	}
	ws.state = state
	return nil
}

func (d *Driver) projectRoot(workspaceID string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if ws, ok := d.workspaces[workspaceID]; ok {
		return ws.projectRoot
	}
	return ""
}

// AgentConn opens a bidirectional net.Conn that speaks the Nexus shell
// protocol over a process-sandboxed shell session. The optional interface is
// checked by the server via type assertion.
func (d *Driver) AgentConn(ctx context.Context, workspaceID string) (net.Conn, error) {
	left, right := net.Pipe()
	go d.serveShellProtocol(context.Background(), workspaceID, right)
	return left, nil
}

func (d *Driver) serveShellProtocol(ctx context.Context, workspaceID string, conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	var writeMu sync.Mutex
	writeJSON := func(msg map[string]any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return enc.Encode(msg)
	}

	type shellSession struct {
		id   string
		ptmx *os.File
	}

	var session *shellSession
	closeSession := func() {
		if session == nil {
			return
		}
		_ = session.ptmx.Close()
		session = nil
	}

	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			closeSession()
			return
		}

		typ, _ := req["type"].(string)
		id, _ := req["id"].(string)

		switch typ {
		case "shell.open":
			closeSession()

			shell, _ := req["command"].(string)
			if shell == "" {
				shell = "bash"
			}
			workdir, _ := req["workdir"].(string)
			repoRoot := d.projectRoot(workspaceID)
			if workdir == "" || workdir == "/workspace" {
				workdir = repoRoot
			}
			if workdir == "" {
				_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 1, "stderr": "no project root for workspace"})
				continue
			}

			cmd, err := sandbox.ShellCommand(shell, workdir, repoRoot)
			if err != nil {
				_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 1, "stderr": err.Error()})
				continue
			}

			ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
			if err != nil {
				_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 1, "stderr": err.Error()})
				continue
			}

			session = &shellSession{id: id, ptmx: ptmx}
			_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 0})

			go func(s *shellSession) {
				buf := make([]byte, 4096)
				for {
					n, readErr := s.ptmx.Read(buf)
					if n > 0 {
						_ = writeJSON(map[string]any{"id": s.id, "type": "chunk", "stream": "stdout", "data": string(buf[:n])})
					}
					if readErr != nil {
						break
					}
				}
				_ = writeJSON(map[string]any{"id": s.id, "type": "result", "exit_code": 0})
			}(session)

		case "shell.write":
			if session == nil {
				_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 1, "stderr": "no active shell session"})
				continue
			}
			data, _ := req["data"].(string)
			if _, err := session.ptmx.Write([]byte(data)); err != nil {
				_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 1, "stderr": err.Error()})
				continue
			}
			_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 0})

		case "shell.resize":
			if session == nil {
				_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 1, "stderr": "no active shell session"})
				continue
			}
			cols := toInt(req["cols"], 120)
			rows := toInt(req["rows"], 30)
			if err := pty.Setsize(session.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
				_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 1, "stderr": err.Error()})
				continue
			}
			_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 0})

		case "shell.close":
			closeSession()
			_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 0})
			return

		default:
			_ = writeJSON(map[string]any{"id": id, "type": "result", "exit_code": 1, "stderr": fmt.Sprintf("unknown request type %q", typ)})
		}
	}
}

func toInt(value any, fallback int) int {
	switch v := value.(type) {
	case float64:
		if int(v) > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	}
	return fallback
}
