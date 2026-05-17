package update

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
	"github.com/oursky/nexus/packages/nexus/internal/tui/pty"
)

// handlePTYOpened handles when a new PTY session is opened.
func handlePTYOpened(msg pty.PtyOpenedMsg, m *model.AppModel) (tea.Model, tea.Cmd) {
	tabs := m.Tabs()

	// Add new active tab
	tabs = append(tabs, model.PTYSession{
		ID:          msg.SessionID,
		Label:       msg.SessionID,
		WorkspaceID: msg.WsID,
	})
	m.SetTabs(tabs)
	m.SetActiveTab(len(tabs) - 1)

	return m, nil
}

// handlePTYData handles PTY output from a terminal session.
func handlePTYData(msg pty.PtyDataMsg, m *model.AppModel) (tea.Model, tea.Cmd) {
	if pp := m.PTYPane(); pp != nil {
		pp.Write(msg.Data)
	}
	return m, nil
}

// handlePTYClosed handles when a PTY session is closed.
func handlePTYClosed(msg pty.PtyClosedMsg, m *model.AppModel) (tea.Model, tea.Cmd) {
	tabs := m.Tabs()
	for i, t := range tabs {
		if t.ID == msg.SessionID {
			tabs = append(tabs[:i], tabs[i+1:]...)
			break
		}
	}
	m.SetTabs(tabs)

	// If closing active tab, switch to another tab
	if m.ActiveTab() >= len(tabs) {
		if len(tabs) > 0 {
			m.SetActiveTab(len(tabs) - 1)
		} else {
			m.SetActiveTab(-1)
		}
	}

	return m, nil
}

// handleTabOpened handles when a new tab is opened.
func handleTabOpened(msg model.TabOpenedMsg, m *model.AppModel) (tea.Model, tea.Cmd) {
	tabs := m.Tabs()

	tabs = append(tabs, model.PTYSession{
		ID:          msg.ID,
		Label:       msg.Label,
		WorkspaceID: msg.WorkspaceID,
	})
	m.SetTabs(tabs)
	m.SetActiveTab(len(tabs) - 1)

	return m, nil
}

// handleTabSelected handles tab switching.
func handleTabSelected(msg model.TabSelectedMsg, m *model.AppModel) (tea.Model, tea.Cmd) {
	tabs := m.Tabs()
	for i, t := range tabs {
		if t.ID == msg.ID {
			m.SetActiveTab(i)
			break
		}
	}

	return m, nil
}

// handleTabClosed handles when a tab is closed.
func handleTabClosed(msg model.TabClosedMsg, m *model.AppModel) (tea.Model, tea.Cmd) {
	tabs := m.Tabs()
	for i, t := range tabs {
		if t.ID == msg.ID {
			tabs = append(tabs[:i], tabs[i+1:]...)
			break
		}
	}
	m.SetTabs(tabs)

	// If closing active tab, switch to another tab
	if m.ActiveTab() >= len(tabs) {
		if len(tabs) > 0 {
			m.SetActiveTab(len(tabs) - 1)
		} else {
			m.SetActiveTab(-1)
		}
	}

	return m, nil
}
