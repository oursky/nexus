package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// StartSync starts a sync session.
func StartSync(mux *rpc.MuxConn, workspaceID string, localPath string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			SessionID string `json:"sessionId"`
			Status    string `json:"status"`
		}
		err := mux.Call("workspace.sync-start", map[string]any{
			"workspaceId": workspaceID,
			"localPath":   localPath,
			"direction":   "bidirectional",
		}, &result)
		if err != nil {
			return messages.DaemonDisconnected{Error: err}
		}
		return messages.SyncStatusReceived{
			WorkspaceID: workspaceID,
			Status:      result.Status,
		}
	}
}

// StopSync stops a sync session.
func StopSync(mux *rpc.MuxConn, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Success bool `json:"success"`
		}
		err := mux.Call("workspace.sync-stop", map[string]any{
			"sessionId":   "",
			"workspaceId": workspaceID,
		}, &result)
		if err != nil {
			return messages.DaemonDisconnected{Error: err}
		}
		return messages.SyncStatusReceived{
			WorkspaceID: workspaceID,
			Status:      "stopped",
		}
	}
}

// PauseSync pauses a sync session.
func PauseSync(mux *rpc.MuxConn, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Success bool `json:"success"`
		}
		err := mux.Call("workspace.sync-pause", map[string]any{
			"sessionId": workspaceID,
		}, &result)
		if err != nil {
			return messages.DaemonDisconnected{Error: err}
		}
		return messages.SyncStatusReceived{
			WorkspaceID: workspaceID,
			Status:      "paused",
		}
	}
}

// ResumeSync resumes a sync session.
func ResumeSync(mux *rpc.MuxConn, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Success bool `json:"success"`
		}
		err := mux.Call("workspace.sync-resume", map[string]any{
			"sessionId": workspaceID,
		}, &result)
		if err != nil {
			return messages.DaemonDisconnected{Error: err}
		}
		return messages.SyncStatusReceived{
			WorkspaceID: workspaceID,
			Status:      "running",
		}
	}
}

// FetchSyncSessions retrieves the sync sessions for a workspace.
func FetchSyncSessions(mux *rpc.MuxConn, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Sessions []struct {
				SessionID   string `json:"sessionId"`
				WorkspaceID string `json:"workspaceId"`
				LocalPath   string `json:"localPath"`
				Status      string `json:"status"`
				Direction   string `json:"direction"`
			} `json:"sessions"`
		}
		err := mux.Call("workspace.sync-list", map[string]any{
			"workspaceId": workspaceID,
		}, &result)
		if err != nil {
			return messages.DaemonDisconnected{Error: err}
		}
		var items []messages.SyncSessionItem
		for _, s := range result.Sessions {
			items = append(items, messages.SyncSessionItem{
				ID:          s.SessionID,
				WorkspaceID: s.WorkspaceID,
				LocalPath:   s.LocalPath,
				Status:      s.Status,
				Direction:   s.Direction,
			})
		}
		return messages.SyncListReceived{Sessions: items}
	}
}
