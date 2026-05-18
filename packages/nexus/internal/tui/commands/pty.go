package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
)

// TabOpenedMsg is sent when a new PTY tab is opened.
type TabOpenedMsg struct {
	ID          string
	Label       string
	WorkspaceID string
}

// TabClosedMsg is sent when a PTY tab is closed.
type TabClosedMsg struct{ ID string }

// PTYSessionManager is the interface the model's PTYSessionManager satisfies.
type PTYSessionManager interface {
	OpenSession(wsID, label string, cols, rows int) (*struct {
		ID          string
		Label       string
		WorkspaceID string
		DataCh      <-chan []byte
	}, tea.Cmd)
	CloseSession(sessionID string)
}

// OpenPTY opens a new PTY session for a workspace.
func OpenPTY(mux *rpc.MuxConn, sessionManager PTYSessionManager, workspaceID, label string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		// Create session
		session, cmd := sessionManager.OpenSession(workspaceID, label, cols, rows)
		if cmd != nil {
			return cmd()
		}

		// TODO: Subscribe to pty.data via RPC
		// Example: dataCh := mux.Subscribe(session.ID + ".pty.data")
		_ = session

		return TabOpenedMsg{
			ID:          workspaceID + "-" + label,
			Label:       label,
			WorkspaceID: workspaceID,
		}
	}
}

// ClosePTY closes a PTY session.
func ClosePTY(mux *rpc.MuxConn, sessionManager PTYSessionManager, sessionID string) tea.Cmd {
	return func() tea.Msg {
		sessionManager.CloseSession(sessionID)

		// TODO: Unsubscribe from pty.data via RPC

		return TabClosedMsg{ID: sessionID}
	}
}

// SendPTYInput sends input to a PTY session.
func SendPTYInput(mux *rpc.MuxConn, sessionID string, data []byte) tea.Cmd {
	return func() tea.Msg {
		// TODO: Send via RPC
		return nil
	}
}

// ResizePTY resizes a PTY session.
func ResizePTY(mux *rpc.MuxConn, sessionID string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		// TODO: Send resize via RPC
		return nil
	}
}
