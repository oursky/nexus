package model

import (
	tea "github.com/charmbracelet/bubbletea"
)

// PTYSession represents a single PTY tab.
type PTYSession struct {
	ID          string
	Label       string
	WorkspaceID string
	Active      bool
}

// UpdateTabs handles tab-related messages.
func (m *AppModel) UpdateTabs(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case TabOpenedMsg:
		m.tabs = append(m.tabs, PTYSession{
			ID:          msg.ID,
			Label:       msg.Label,
			WorkspaceID: msg.WorkspaceID,
		})
		m.activeTab = len(m.tabs) - 1
		return nil

	case TabSelectedMsg:
		for i, tab := range m.tabs {
			if tab.ID == msg.ID {
				m.activeTab = i
				break
			}
		}
		return nil

	case TabClosedMsg:
		// Remove tab
		for i, tab := range m.tabs {
			if tab.ID == msg.ID {
				m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
				if m.activeTab >= len(m.tabs) {
					m.activeTab = len(m.tabs) - 1
				}
				break
			}
		}
		return nil
	}

	return nil
}

// TabOpenedMsg is sent when a new tab is opened.
type TabOpenedMsg struct {
	ID          string
	Label       string
	WorkspaceID string
}

// TabSelectedMsg is sent when a tab is selected.
type TabSelectedMsg struct {
	ID string
}

// TabClosedMsg is sent when a tab is closed.
type TabClosedMsg struct {
	ID string
}
