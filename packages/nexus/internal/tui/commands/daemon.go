package commands

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// OpenDaemonConn returns a command that ensures the RPC mux is ready.
// It returns a ConnReady message on success or ConnErr on failure.
func OpenDaemonConn() tea.Cmd {
	return func() tea.Msg {
		mux, err := rpc.EnsureMux()
		if err != nil {
			return messages.ConnErr{Err: err}
		}
		return messages.ConnReady{Mux: mux}
	}
}
