package views

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/bubbles/textinput"
	// "github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// CreateView is the workspace creation wizard.
// It guides users through creating a new workspace from a repository.
type CreateView struct {
	width  int
	height int
	step   int // 0: repo, 1: name, 2: isolation, 3: creating

	repoInput      textinput.Model
	nameInput      textinput.Model
	isolationLevel string // "process", "libkrun"
}

// NewCreateView creates a new create view instance.
func NewCreateView(width, height int) *CreateView {
	repoInput := textinput.New()
	repoInput.Placeholder = "https://github.com/user/repo"
	repoInput.Focus()

	nameInput := textinput.New()
	nameInput.Placeholder = "my-workspace"

	return &CreateView{
		width:          width,
		height:         height,
		step:           0,
		repoInput:      repoInput,
		nameInput:      nameInput,
		isolationLevel: "process",
	}
}

// Update handles user input in the create view.
func (v *CreateView) Update(msg tea.Msg, m *model.AppModel) (*model.AppModel, tea.Cmd) {
	result, cmd := m, tea.Cmd(nil)

	// Handle step transitions
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyEnter {
		switch v.step {
		case 0:
			v.step = 1
			v.nameInput.Focus()
			v.repoInput.Blur()
		case 1:
			v.step = 2
		case 2:
			v.step = 3
			// TODO: Emit WorkspaceCreateRequested via commands
			// This will be implemented in Wave 7 (T16-T18)
		case 3:
			// Creating state — no input
		}
	}

	// Update active input
	switch v.step {
	case 0:
		var c tea.Cmd
		v.repoInput, c = v.repoInput.Update(msg)
		result = m
		cmd = c
	case 1:
		var c tea.Cmd
		v.nameInput, c = v.nameInput.Update(msg)
		result = m
		cmd = c
	case 2:
		// Isolation selection
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			if keyMsg.Type == tea.KeyUp || string(keyMsg.Runes) == "k" {
				v.isolationLevel = "process"
			} else if keyMsg.Type == tea.KeyDown || string(keyMsg.Runes) == "j" {
				v.isolationLevel = "libkrun"
			}
		}
	case 3:
		// Creating state — no input
	}

	return result, cmd
}

// View renders the workspace creation wizard.
func (v *CreateView) View(m *model.AppModel) string {
	// TODO: Implement full create rendering with:
	// - Step indicator
	// - Repository URL input (step 0)
	// - Workspace name input (step 1)
	// - Isolation level selection (step 2)
	// - Creating spinner (step 3)
	// - Validation errors

	// Placeholder for now — Wave 7 will implement full rendering
	return lipgloss.NewStyle().
		Width(v.width).
		Height(v.height).
		Render("Create view — TODO in Wave 7")
}

// SetSize updates the view dimensions.
func (v *CreateView) SetSize(width, height int) {
	v.width = width
	v.height = height
}
