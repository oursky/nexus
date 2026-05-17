package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// HelpView displays keyboard shortcuts and help information.
// It's rendered as a modal overlay when requested.
type HelpView struct{}

// NewHelpView creates a new help view instance.
func NewHelpView() *HelpView {
	return &HelpView{}
}

// View renders the help overlay.
func (v *HelpView) View(m *model.AppModel) string {
	colors := design.ActiveTheme.Colors
	width := m.Width()

	title := design.Title.Render("⌨  Keyboard Shortcuts")

	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(colors.Accent).MarginTop(1)
	keyStyle := lipgloss.NewStyle().Foreground(colors.Text).Bold(true).Width(12)
	descStyle := lipgloss.NewStyle().Foreground(colors.TextMuted)

	sections := []struct {
		Title string
		Items []struct{ Key, Desc string }
	}{
		{"Navigation", []struct{ Key, Desc string }{
			{"↑/k", "Move up"},
			{"↓/j", "Move down"},
			{"Enter", "Select/Open"},
			{"Esc", "Back/Cancel"},
		}},
		{"Workspace", []struct{ Key, Desc string }{
			{"s", "Start workspace"},
			{"x", "Stop workspace"},
			{"d", "Delete workspace"},
			{"f", "Fork workspace"},
			{"t", "Open terminal"},
		}},
		{"Views", []struct{ Key, Desc string }{
			{"/", "Filter workspaces"},
			{"l", "Spotlight (port forward)"},
			{"y", "Sync"},
			{"c", "Create workspace"},
			{"?", "Help"},
		}},
		{"General", []struct{ Key, Desc string }{
			{"q", "Quit"},
			{"Ctrl+C", "Force quit"},
		}},
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	for _, sec := range sections {
		b.WriteString(sectionStyle.Render(sec.Title))
		b.WriteString("\n")
		for _, item := range sec.Items {
			b.WriteString("  ")
			b.WriteString(keyStyle.Render(item.Key))
			b.WriteString(descStyle.Render(item.Desc))
			b.WriteString("\n")
		}
	}

	hintStyle := lipgloss.NewStyle().Foreground(colors.TextMuted).Italic(true)
	b.WriteString("\n")
	b.WriteString(hintStyle.Render("Press Esc or q to close"))

	return lipgloss.NewStyle().
		Padding(design.SpaceMD).
		Width(width).
		Height(m.Height() - 4).
		Render(b.String())
}
