package views

import (
	"github.com/charmbracelet/lipgloss"
)

// HelpConfig holds configuration for rendering the help overlay.
type HelpConfig struct {
	Width int
}

// RenderHelp renders the key reference help overlay.
func RenderHelp(cfg HelpConfig) string {
	s := `Key reference

Global
  q           Quit
  ?           Toggle this help
  r           Refresh workspace list
  /           Filter workspaces (when list is focused)
  1-9         Jump-select session tab N in the workspace list

Main list
  n / +       New workspace (wizard)
  enter       Open workspace detail
  t           Attach terminal (nexus workspace shell)
  s x d       Start / stop / delete selected workspace

Detail view
  esc enter   Back to list
  c           Connect (SSH / editor hints)
  l           Spotlight forwards
  y           Volume sync sessions
  t           Terminal (runs: nexus workspace shell)
  f           Fork workspace
  s x d       Start / stop / delete

Tab bar (appears after first terminal attach)
  Persistent across sessions via ~/.local/state/nexus/tui-sessions.json
  Use --auto-attach flag to re-enter last shell on startup
 `
	return lipgloss.NewStyle().MaxWidth(cfg.Width).Render(s)
}
