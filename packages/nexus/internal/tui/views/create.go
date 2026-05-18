package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// CreateWizardConfig holds configuration for rendering the create workspace wizard.
type CreateWizardConfig struct {
	Width   int
	NameTI  textinput.Model
	RepoTI  textinput.Model
	RefTI   textinput.Model
}

// RenderCreateWizard renders the create workspace wizard.
func RenderCreateWizard(cfg CreateWizardConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("New workspace"))
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Name"))
	fmt.Fprintf(&b, "%s\n\n", cfg.NameTI.View())
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Repo path"))
	fmt.Fprintf(&b, "%s\n\n", cfg.RepoTI.View())
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Ref"))
	fmt.Fprintf(&b, "%s\n", cfg.RefTI.View())
	return lipgloss.NewStyle().MaxWidth(cfg.Width).Render(b.String())
}
