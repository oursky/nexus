package commands

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// FetchWorkspaces calls workspace.list via RPC and returns the workspace list.
func FetchWorkspaces(mux Mux) ([]workspace.Workspace, error) {
	var result struct {
		Workspaces []*workspace.Workspace `json:"workspaces"`
	}
	if err := mux.Call("workspace.list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	out := make([]workspace.Workspace, 0, len(result.Workspaces))
	for _, p := range result.Workspaces {
		if p == nil {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

// FetchProjectsByID calls project.list and returns a map of project ID → name.
// Returns nil on error so callers can treat it as best-effort.
func FetchProjectsByID(mux Mux) map[string]string {
	var result struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := mux.Call("project.list", map[string]any{}, &result); err != nil {
		return nil
	}
	m := make(map[string]string, len(result.Projects))
	for _, p := range result.Projects {
		m[p.ID] = p.Name
	}
	return m
}

// RefreshCmd returns a command that fetches the workspace list and project list
// in parallel and delivers a Workspaces message.
func RefreshCmd(mux Mux) tea.Cmd {
	if mux == nil {
		return nil
	}
	return func() tea.Msg {
		// Fetch workspace list and project list in parallel.
		type wsResult struct {
			ws  []workspace.Workspace
			err error
		}
		type projResult struct {
			byID map[string]string
		}
		wsCh := make(chan wsResult, 1)
		projCh := make(chan projResult, 1)
		go func() {
			ws, err := FetchWorkspaces(mux)
			wsCh <- wsResult{ws, err}
		}()
		go func() {
			projCh <- projResult{FetchProjectsByID(mux)}
		}()
		wr := <-wsCh
		if wr.err != nil {
			return messages.WorkspacesErr{Err: wr.err}
		}
		pr := <-projCh
		return messages.Workspaces{Workspaces: wr.ws, ProjectsByID: pr.byID}
	}
}

// LoadDetailCmd returns a command that fetches workspace details via RPC
// and delivers a DetailLoaded message.
func LoadDetailCmd(mux Mux, id string) tea.Cmd {
	if mux == nil {
		return nil
	}
	return func() tea.Msg {
		var result struct {
			Workspace *workspace.Workspace `json:"workspace"`
		}
		if err := mux.Call("workspace.info", map[string]any{"id": id}, &result); err != nil {
			return messages.DetailErr{Err: err}
		}
		if result.Workspace == nil {
			return messages.DetailErr{Err: fmt.Errorf("workspace.info: empty workspace")}
		}
		return messages.DetailLoaded{Ws: *result.Workspace}
	}
}

// RpcVoidCmd returns a command that performs a void RPC call (no result expected)
// and delivers a MutationDone message.
func RpcVoidCmd(mux Mux, method string, id string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call(method, map[string]any{"id": id}, nil); err != nil {
			return messages.MutationDone{Err: err}
		}
		return messages.MutationDone{Err: nil}
	}
}

// CreateWorkspaceCmd returns a command that creates a new workspace via RPC
// and delivers a CreateDone message.
func CreateWorkspaceCmd(mux Mux, spec workspace.CreateSpec) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.CreateDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.create", map[string]any{"spec": spec}, nil); err != nil {
			return messages.CreateDone{Err: err}
		}
		return messages.CreateDone{Err: nil}
	}
}

// ForkCmd returns a command that forks a workspace via RPC
// and delivers a ForkDone message.
func ForkCmd(mux Mux, parentID, childName string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.ForkDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.fork", map[string]any{
			"id":                 parentID,
			"childWorkspaceName": childName,
		}, nil); err != nil {
			return messages.ForkDone{Err: err}
		}
		return messages.ForkDone{Err: nil}
	}
}

// ShellExecCmd returns a command that executes a shell for the given workspace
// by spawning an external nexus process. Delivers a ShellReturned message on exit.
func ShellExecCmd(wsID string) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = "nexus"
	}
	c := exec.Command(exe, "workspace", "shell", wsID)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = os.Environ()
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return messages.ShellReturned{Err: err, WsID: wsID}
	})
}
