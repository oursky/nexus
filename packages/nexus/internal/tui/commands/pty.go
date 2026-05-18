package commands

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
)

// TickRefresh returns a command that ticks every 5 seconds to trigger
// workspace list refreshes.
func TickRefresh() tea.Cmd {
	return tea.Every(5*time.Second, func(time.Time) tea.Msg {
		return messages.RefreshTick{}
	})
}

// PtyMouseModeCmd returns the tea.Cmd to adjust mouse capture when ptyFocused
// transitions from oldFocused to newFocused.
//
// When the PTY pane gains focus Bubble Tea's all-motion mouse capture is
// disabled so the host terminal emulator can perform native text selection via
// click-drag. When focus leaves the PTY pane, all-motion capture is restored so
// TUI click navigation (pane switching, list scrolling) keeps working.
//
// Returns nil when the focus state has not changed (safe to batch unconditionally).
func PtyMouseModeCmd(oldFocused, newFocused bool) tea.Cmd {
	if oldFocused == newFocused {
		return nil
	}
	if newFocused {
		return tea.DisableMouse
	}
	return tea.EnableMouseAllMotion
}
