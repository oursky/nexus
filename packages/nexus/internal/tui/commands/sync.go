package commands

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// LoadSyncListCmd returns a command that fetches the list of sync sessions
// for a workspace and delivers a SyncListData message.
func LoadSyncListCmd(mux Mux, wsID string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.SyncListData{Err: fmt.Errorf("not connected")}
		}
		var result struct {
			Sessions []struct {
				SessionID   string `json:"sessionId"`
				WorkspaceID string `json:"workspaceId"`
				LocalPath   string `json:"localPath"`
				Status      string `json:"status"`
				Direction   string `json:"direction"`
			} `json:"sessions"`
		}
		if err := mux.Call("workspace.sync-list", map[string]any{"workspaceId": wsID}, &result); err != nil {
			return messages.SyncListData{Err: err}
		}
		rows := make([]messages.SyncRow, 0, len(result.Sessions))
		for _, s := range result.Sessions {
			rows = append(rows, messages.SyncRow{
				SessionID:   s.SessionID,
				WorkspaceID: s.WorkspaceID,
				LocalPath:   s.LocalPath,
				Status:      s.Status,
				Direction:   s.Direction,
			})
		}
		return messages.SyncListData{Rows: rows}
	}
}

// SyncStartCmd returns a command that starts a sync session.
func SyncStartCmd(mux Mux, wsID, localPath, direction string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.sync-start", map[string]any{
			"workspaceId": wsID,
			"localPath":   localPath,
			"direction":   direction,
		}, nil); err != nil {
			return messages.MutationDone{Err: err}
		}
		return messages.MutationDone{Err: nil}
	}
}

// SyncStopSessionCmd returns a command that stops a sync session.
func SyncStopSessionCmd(mux Mux, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.sync-stop", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return messages.MutationDone{Err: err}
		}
		return messages.MutationDone{Err: nil}
	}
}

// SyncPauseCmd returns a command that pauses a sync session.
func SyncPauseCmd(mux Mux, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.sync-pause", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return messages.MutationDone{Err: err}
		}
		return messages.MutationDone{Err: nil}
	}
}

// SyncResumeCmd returns a command that resumes a paused sync session.
func SyncResumeCmd(mux Mux, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return messages.MutationDone{Err: fmt.Errorf("not connected")}
		}
		if err := mux.Call("workspace.sync-resume", map[string]any{"sessionId": sessionID}, nil); err != nil {
			return messages.MutationDone{Err: err}
		}
		return messages.MutationDone{Err: nil}
	}
}
