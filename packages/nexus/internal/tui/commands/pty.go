package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
)

// OpenPTY opens a new PTY session for a workspace.
func OpenPTY(mux *rpc.MuxConn, sessionManager *model.PTYSessionManager, workspaceID, label string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		// Create session
		session, cmd := sessionManager.OpenSession(workspaceID, label, cols, rows)
		if cmd != nil {
			return cmd()
		}

		// TODO: Subscribe to pty.data via RPC
		// Example: dataCh := mux.Subscribe(session.ID + ".pty.data")
		session.DataCh = make(chan []byte, 1024)

		return model.TabOpenedMsg{
			ID:          session.ID,
			Label:       label,
			WorkspaceID: workspaceID,
		}
	}
}

// ClosePTY closes a PTY session.
func ClosePTY(mux *rpc.MuxConn, sessionManager *model.PTYSessionManager, sessionID string) tea.Cmd {
	return func() tea.Msg {
		sessionManager.CloseSession(sessionID)

		// TODO: Unsubscribe from pty.data via RPC

		return model.TabClosedMsg{ID: sessionID}
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
