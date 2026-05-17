package components

import (
	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
)

type Modal struct {
	Title    string
	Body     string
	Buttons  []string
	Active   int
	Visible  bool
	Width    int
	Height   int
}

type ShowModalMsg struct {
	Title   string
	Body    string
	Buttons []string
}

type HideModalMsg struct{}

type ModalConfirmedMsg struct {
	Index int
	Label string
}

func ShowModal(title, body string, buttons []string) tea.Cmd {
	return func() tea.Msg {
		return ShowModalMsg{Title: title, Body: body, Buttons: buttons}
	}
}

func HideModal() tea.Cmd {
	return func() tea.Msg {
		return HideModalMsg{}
	}
}

func (m *Modal) Update(msg tea.Msg) tea.Cmd {
	if !m.Visible {
		return nil
	}

	switch msg := msg.(type) {
	case ShowModalMsg:
		m.Title = msg.Title
		m.Body = msg.Body
		m.Buttons = msg.Buttons
		m.Active = 0
		m.Visible = true
		return nil

	case HideModalMsg:
		m.Visible = false
		return nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyLeft:
			if m.Active > 0 {
				m.Active--
			}
		case tea.KeyRight:
			if m.Active < len(m.Buttons)-1 {
				m.Active++
			}
		case tea.KeyEnter, tea.KeySpace:
			return func() tea.Msg {
				return ModalConfirmedMsg{Index: m.Active, Label: m.Buttons[m.Active]}
			}
		case tea.KeyEsc, tea.KeyCtrlC:
			return HideModal()
		}
	}

	return nil
}

func (m *Modal) View() string {
	if !m.Visible {
		return ""
	}

	c := design.ActiveTheme.Colors

	// Calculate modal dimensions (max 60 chars wide, respecting available space)
	width := min(60, m.Width-design.SpaceXL*2)
	if width < 20 {
		width = 20
	}

	// Build button row
	buttons := make([]string, len(m.Buttons))
	for i, btn := range m.Buttons {
		state := ButtonNormal
		if i == m.Active {
			state = ButtonActive
		}
		b := &Button{Label: btn, State: state}
		buttons[i] = b.View()
	}

	// Render modal
	modalContent := lipgloss.JoinVertical(
		lipgloss.Left,
		design.Title.Render(m.Title),
		"",
		design.Body.Render(m.Body),
		"",
		lipgloss.JoinHorizontal(lipgloss.Left, buttons...),
	)

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c.BorderFocus).
		Background(c.BgSubtle).
		Padding(design.SpaceMD, design.SpaceLG).
		Width(width)

	modal := modalStyle.Render(modalContent)

	// Center modal in viewport
	if m.Height > 0 && m.Width > 0 {
		return lipgloss.Place(
			m.Width, m.Height,
			lipgloss.Center, lipgloss.Center,
			modal,
			lipgloss.WithWhitespaceChars(" "),
			lipgloss.WithWhitespaceForeground(c.Bg),
		)
	}

	return modal
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
