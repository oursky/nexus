package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

func (m Model) renderFooterHelp(innerWidth int) string {
	var raw string
	switch {
	case m.showNoProfile:
		if m.noProfileChecking || m.noProfileBusy {
			raw = "esc quit"
		} else {
			raw = "↑↓ j/k navigate   ↵ select   esc quit"
		}
	case m.showWizard:
		raw = "tab next field   ↵ connect   esc skip (localhost)"
	case m.panel == panelSpotlight:
		raw = "esc/l close   n new port   x remove   j/k navigate"
	case m.panel == panelSync:
		raw = "esc/y close   n new   x stop   p pause   r resume   j/k navigate"
	case m.panel == panelConnect:
		raw = "esc/c close"
	case m.createMode:
		raw = "tab next field   ↵ next/submit   esc cancel"
	case m.detail != nil && m.panel == panelNone && !m.showHelp:
		raw = "q quit   ? help   esc back   r refresh   c connect   l spotlight   y sync   t terminal   f fork   s/x/d"
	case m.sidebarFocused && m.isThreePaneMode():
		raw = "tab→left  n add  x del  j/k navigate  l unfocus"
	case m.ptyFocused && m.ptyPane != nil:
		if m.isThreePaneMode() {
			raw = "ctrl+a→spotlight   t full-screen shell"
		} else {
			raw = "ctrl+a unfocus terminal   t full-screen shell"
		}
	default:
		if m.isThreePaneMode() {
			switch {
			case m.width < 140:
				raw = "q quit   ? help   tab focus PTY   l spotlight   t full-screen"
			default:
				raw = "q quit   ? help   tab focus PTY   l spotlight   t full-screen   ↵ detail   n new   / filter   s start   x stop   d delete"
			}
		} else if m.isSplitMode() {
			switch {
			case m.width < 120:
				raw = "q quit   ? help   tab focus terminal   t full-screen"
			default:
				raw = "q quit   ? help   tab focus terminal   t full-screen   ↵ detail   n new   / filter   s start   x stop   d delete"
			}
		} else {
			switch {
			case m.width < 60:
				raw = "q  ?"
			case m.width < 80:
				raw = "q quit   ? help   ↵ open   esc back"
			default:
				raw = "q quit   ? help   ↵ detail   t terminal   esc back   r refresh   n new   / filter   s start   x stop   d delete   1-9 tab"
			}
		}
	}
	// Reserve 2 chars for the leading "  " prefix; clip to fit inner width.
	clipped := truncate(raw, max(innerWidth-2, 1))
	return mutedStyle.Render("  " + clipped)
}

// View implements tea.Model.

func (m Model) View() string {
	var statusDaemon string
	if m.quitting {
		statusDaemon = mutedStyle.Render("● exiting")
	} else if m.showNoProfile {
		statusDaemon = mutedStyle.Render("● setup")
	} else if m.showWizard {
		statusDaemon = mutedStyle.Render("● setup")
	} else if m.mux != nil && m.daemonOK {
		statusDaemon = statusOkStyle.Render("● connected")
	} else if m.mux != nil && !m.daemonOK {
		statusDaemon = warningStyle.Render("● degraded")
	} else {
		statusDaemon = statusErrStyle.Render("● disconnected")
	}

	headerTop := lipgloss.JoinHorizontal(lipgloss.Left,
		accentStyle.Render("nexus"),
		mutedStyle.Render("  "),
		statusDaemon,
	)
	headerBottom := ""
	if strings.TrimSpace(m.statusLine) != "" {
		headerBottom = mutedStyle.Render(truncate(m.statusLine, max(m.width-4, 40)))
	}

	body := ""
	// Use split-pane layout on wide terminals when no full-width overlay is
	// active (detail view, help, panels, create wizard, etc.).
	useSplit := m.isSplitMode() && !m.blocksOverlayInput() && m.detail == nil &&
		!m.showHelp && !m.createMode
	switch {
	case m.showNoProfile:
		body = renderNoProfile(&m)
	case m.showWizard:
		body = renderWizard(&m)
	case m.createMode:
		body = renderCreateWizard(&m, m.listWidth)
	case m.showHelp:
		body = renderHelp(m.listWidth)
	case m.panel == panelConnect && m.detail != nil:
		body = renderConnectPanel(m.detail, m.listWidth)
	case m.panel == panelSpotlight && m.detail != nil:
		body = renderSpotlightPanel(&m, m.listWidth)
	case m.panel == panelSync && m.detail != nil:
		body = renderSyncPanel(&m, m.listWidth)
	case m.detail != nil:
		body = renderWorkspaceDetail(m.detail, m.listWidth)
	case m.prompt == promptSidebarSpotPort:
		// Centered modal overlay for the sidebar port-forward form.
		body = m.renderSidebarPortModal()
	case useSplit:
		body = m.renderSplitLayout()
	default:
		body = m.list.View()
	}

	// Generic prompt overlay (not promptSidebarSpotPort which has its own modal).
	if m.prompt != promptNone && m.prompt != promptSidebarSpotPort {
		body = lipgloss.JoinVertical(lipgloss.Left,
			body,
			"",
			titleStyle.Render("Input"),
			m.promptInput.View(),
		)
	}

	innerWidth := max(m.width-4, 20)

	confirm := ""
	if m.confirmDelete {
		confirm = warningStyle.Render(fmt.Sprintf("Delete workspace %s? [y/N]", truncate(m.pendingDeleteID, 36)))
	}

	sep := separatorStyle.Render(strings.Repeat("─", innerWidth))
	footerHelp := m.renderFooterHelp(innerWidth)
	footer := lipgloss.JoinVertical(lipgloss.Left, sep, confirm, footerHelp)

	// Build the vertical stack. Insert the tab bar when present.
	tabBar := m.renderTabBar()
	tabBarH := 0
	rows := []string{headerTop, headerBottom}
	if tabBar != "" {
		tabBarH = 2
		rows = append(rows, tabSepStyle.Render(strings.Repeat("─", innerWidth)))
		rows = append(rows, tabBar)
	}

	// Sticky footer: pad body to exactly fill the remaining height so that
	// the footer always appears at the very bottom of the screen.
	// Overhead: 2 header + tabBarH + 2 spacers (blank lines around body) + 3 footer = 7 + tabBarH.
	if m.height > 0 {
		contentH := m.height - 7 - tabBarH
		if contentH < 1 {
			contentH = 1
		}
		body = lipgloss.NewStyle().Height(contentH).Render(body)
	}

	rows = append(rows, "", body, "", footer)

	box := lipgloss.NewStyle().
		MaxWidth(m.width).
		Padding(0, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, rows...))

	return box
}

// renderSidebarPortModal renders a centered modal overlay for the "forward
// remote port" input, used when the user presses n in the spotlight sidebar.

func (m Model) renderSidebarPortModal() string {
	portVal := strings.TrimSpace(m.promptInput.Value())
	var hint string
	if p, err := strconv.Atoi(portVal); err == nil && p > 0 && p <= 65535 {
		hint = mutedStyle.Render(fmt.Sprintf("→ localhost:%d will forward to workspace port %d", p, p))
	} else {
		hint = mutedStyle.Render("Enter the port number exposed inside the workspace")
	}

	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Forward remote port"),
		"",
		mutedStyle.Render("Remote port:"),
		m.promptInput.View(),
		"",
		hint,
		"",
		mutedStyle.Render("↵ forward   esc cancel"),
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 3).
		Width(50).
		Render(inner)

	availW := max(m.width-4, 58)
	availH := max(m.height-7-2*m.tabBarOffset(), 10)
	return lipgloss.Place(availW, availH, lipgloss.Center, lipgloss.Center, box)
}

// renderSplitLayout renders the split-pane view.
//
// Two-pane  (90–109 cols): workspace list (28%) | PTY (72%).
// Three-pane (≥110 cols):  workspace list (28%) | PTY (≈50%) | spotlight (22%).
//
// Both sidebars have a RoundedBorder; the center PTY pane uses separator lines.

func (m Model) renderSplitLayout() string {
	leftW, centerW, rightW := m.paneDimensions()
	bodyH := max(m.height-7-2*m.tabBarOffset(), 4)

	// Left pane inner dimensions (inside RoundedBorder).
	leftInnerW := max(leftW-2, 4)
	leftInnerH := max(bodyH-2, 2)

	// Border colour: accent when left pane has implicit focus (nothing else focused).
	var leftBorderColor lipgloss.TerminalColor = colorBorder
	if !m.ptyFocused && !m.sidebarFocused {
		leftBorderColor = colorAccent
	}

	leftContent := m.list.View()
	leftPane := lipgloss.NewStyle().
		Width(leftInnerW).
		Height(leftInnerH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(leftBorderColor).
		Render(leftContent)

	// Center pane content: PTY or workspace info.
	var centerContent string
	if m.ptyPane != nil {
		centerContent = m.ptyPane.Render()
	} else {
		selWS := m.selectedWorkspace()
		switch {
		case selWS != nil && selWS.State == workspace.StateRunning:
			centerContent = mutedStyle.Render("Opening terminal…")
		case selWS != nil:
			centerContent = renderWorkspaceDetail(selWS, centerW)
		default:
			centerContent = mutedStyle.Render("Select a workspace")
		}
	}

	if !m.isThreePaneMode() {
		// Two-pane: left pane has full border; center uses a left separator line.
		var centerBorderColor lipgloss.TerminalColor = colorBorder
		if m.ptyFocused {
			centerBorderColor = colorAccent
		}
		centerInnerW := max(centerW-1, 4) // -1 for left border
		centerPane := lipgloss.NewStyle().
			Width(centerInnerW).
			Height(bodyH).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(centerBorderColor).
			Render(centerContent)
		return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, centerPane)
	}

	// Three-pane layout.
	centerPane := lipgloss.NewStyle().Width(centerW).Height(bodyH).Render(centerContent)

	// Separator between center and right pane: accent when spotlight is focused.
	sep2Color := colorBorder
	if m.sidebarFocused {
		sep2Color = colorAccent
	}
	sep2 := lipgloss.NewStyle().Foreground(sep2Color).Render("│")

	// Right pane inner dimensions (inside RoundedBorder).
	rightInnerW := max(rightW-2, 4)
	rightInnerH := max(bodyH-2, 2)

	var rightBorderColor lipgloss.TerminalColor = colorBorder
	if m.sidebarFocused {
		rightBorderColor = colorAccent
	}

	spotlightContent := renderSpotlightSidebar(&m, rightInnerW, rightInnerH)
	rightPane := lipgloss.NewStyle().
		Width(rightInnerW).
		Height(rightInnerH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rightBorderColor).
		Render(spotlightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, centerPane, sep2, rightPane)
}

// renderTabBar renders the session tab bar line.
// Returns an empty string when there are no tabs.

func (m Model) renderTabBar() string {
	if len(m.tabs) == 0 {
		return ""
	}

	var parts []string
	for i, wsID := range m.tabs {
		if i >= 9 {
			break
		}
		num := fmt.Sprintf("[%d]", i+1)

		// Look up workspace name and state from the current list.
		name := truncate(wsID, 14)
		badge := mutedStyle.Render("○")
		for _, it := range m.list.Items() {
			wi, ok := it.(workspaceItem)
			if !ok || wi.w.ID != wsID {
				continue
			}
			if wi.w.WorkspaceName != "" {
				name = truncate(wi.w.WorkspaceName, 14)
			}
			badge = tabStateBadge(wi.w.State)
			break
		}

		label := num + " " + name + " " + badge
		if wsID == m.activeTabWS {
			parts = append(parts, activeTabStyle.Render(label))
		} else {
			parts = append(parts, inactiveTabStyle.Render(label))
		}
	}

	// [+] hint — press n or + to create a new workspace.
	parts = append(parts, mutedStyle.Render("[+]"))

	return " " + strings.Join(parts, "  ")
}

// tabStateBadge returns a compact coloured state symbol for use in the tab bar.

func tabStateBadge(state workspace.State) string {
	switch state {
	case workspace.StateRunning:
		return statusOkStyle.Render("●")
	case workspace.StateStarting, workspace.StateSnapshotting, workspace.StateRestored:
		return warningStyle.Render("⟳")
	case workspace.StateStopped, workspace.StatePaused, workspace.StateCreated:
		return mutedStyle.Render("○")
	default:
		return mutedStyle.Render("?")
	}
}

func renderCreateWizard(m *Model, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("New workspace"))
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Name"))
	fmt.Fprintf(&b, "%s\n\n", m.nameTI.View())
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Repo path"))
	fmt.Fprintf(&b, "%s\n\n", m.repoTI.View())
	fmt.Fprintf(&b, "%s\n", detailKeyStyle.Render("Ref"))
	fmt.Fprintf(&b, "%s\n", m.refTI.View())
	return lipgloss.NewStyle().MaxWidth(width).Render(b.String())
}

func renderHelp(width int) string {
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
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func renderConnectPanel(ws *workspace.Workspace, width int) string {
	info := buildConnectInstructions(ws)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Connect (SSH / editors)"))
	if info.ProxyJump != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("ProxyJump"), info.ProxyJump)
	}
	if info.GuestTarget != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("SSH target"), info.GuestTarget)
	}
	if info.HostAlias != "" {
		fmt.Fprintf(&b, "%s %s\n", detailKeyStyle.Render("SSH config Host"), info.HostAlias)
	}
	fmt.Fprintf(&b, "\n%s\n", detailKeyStyle.Render("SSH command (copy)"))
	fmt.Fprintf(&b, "%s\n\n", detailValStyle.Render(info.SSHCommand))
	if info.ProcessShellHint != "" {
		fmt.Fprintf(&b, "%s\n%s\n\n", mutedStyle.Render(info.ProcessShellHint), detailValStyle.Render(strings.TrimSpace(info.SSHCommand)))
	}
	fmt.Fprintf(&b, "%s\n%s\n\n", detailKeyStyle.Render("VS Code"), detailValStyle.Render(info.VSCodiumHint))
	fmt.Fprintf(&b, "%s\n%s\n", detailKeyStyle.Render("Cursor"), detailValStyle.Render(info.CursorHint))
	for _, n := range info.Notes {
		if n != "" {
			fmt.Fprintf(&b, "\n%s\n", warningStyle.Render(n))
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(b.String())
}

func renderSpotlightPanel(m *Model, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Spotlight forwards"))
	if len(m.spotForwards) == 0 {
		b.WriteString(mutedStyle.Render("No active forwards. Press n to add (remote port).\n"))
	} else {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render("j/k select · n new · x remove forward"))
		for i, f := range m.spotForwards {
			if f == nil {
				continue
			}
			// Show from user perspective: local port they connect to.
			var portPart string
			if f.LocalPort == f.RemotePort {
				portPart = fmt.Sprintf("localhost:%d", f.LocalPort)
			} else {
				portPart = fmt.Sprintf("localhost:%d → :%d", f.LocalPort, f.RemotePort)
			}
			stateStr := string(f.State)
			if i == m.spotSel {
				icon := "○"
				if f.State == spotlight.ForwardStateActive {
					icon = "●"
				}
				sel := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5"))
				fmt.Fprintf(&b, "%s\n", sel.Render(fmt.Sprintf("› %s  %s  (%s)", icon, portPart, stateStr)))
			} else {
				var icon string
				if f.State == spotlight.ForwardStateActive {
					icon = statusOkStyle.Render("●")
				} else {
					icon = mutedStyle.Render("○")
				}
				fmt.Fprintf(&b, "  %s  %s  %s\n", icon, portPart, mutedStyle.Render("("+stateStr+")"))
			}
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(b.String())
}

func renderSyncPanel(m *Model, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("Volume sync"))
	if len(m.syncRows) == 0 {
		b.WriteString(mutedStyle.Render("No sync sessions. Press n to start (local path + direction).\n"))
	} else {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render("j/k select · n new · x stop · p pause · r resume"))
		for i, r := range m.syncRows {
			line := fmt.Sprintf("%s  %s  %-12s  %s → %s", r.SessionID, r.Status, r.Direction, r.LocalPath, r.WorkspaceID)
			if i == m.syncSel {
				fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render("› "+truncate(line, 72)))
			} else {
				fmt.Fprintf(&b, "%s\n", truncate(line, 72))
			}
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(b.String())
}

// ptyMouseModeCmd returns the tea.Cmd to adjust mouse capture when ptyFocused
// transitions from oldFocused to newFocused.
//
// When the PTY pane gains focus Bubble Tea's all-motion mouse capture is
// disabled so the host terminal emulator can perform native text selection via
// click-drag. When focus leaves the PTY pane, all-motion capture is restored so
// TUI click navigation (pane switching, list scrolling) keeps working.
//
// Returns nil when the focus state has not changed (safe to batch unconditionally).
func renderWorkspaceDetail(ws *workspace.Workspace, width int) string {
	if ws == nil {
		return ""
	}
	keyw := 18
	row := func(k, v string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			detailKeyStyle.Width(keyw).Render(k),
			detailValStyle.Width(max(width-keyw-4, 20)).Render(v),
		)
	}
	ports := "(none)"
	if len(ws.TunnelPorts) > 0 {
		parts := make([]string, len(ws.TunnelPorts))
		for i, p := range ws.TunnelPorts {
			parts[i] = fmt.Sprintf("%d", p)
		}
		ports = strings.Join(parts, ", ")
	}
	b := strings.Builder{}
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render(ws.WorkspaceName))
	fmt.Fprintf(&b, "%s\n", row("ID", ws.ID))
	fmt.Fprintf(&b, "%s\n", row("State", string(ws.State)))
	fmt.Fprintf(&b, "%s\n", row("Backend", ws.Backend))
	fmt.Fprintf(&b, "%s\n", row("Guest IP", ws.GuestIP))
	fmt.Fprintf(&b, "%s\n", row("Repo", truncate(ws.Repo, 60)))
	fmt.Fprintf(&b, "%s\n", row("Ref", ws.Ref))
	fmt.Fprintf(&b, "%s\n", row("Root path", truncate(ws.RootPath, 60)))
	fmt.Fprintf(&b, "%s\n", row("Ports", ports))
	created := "—"
	if !ws.CreatedAt.IsZero() {
		created = ws.CreatedAt.UTC().Format(time.RFC3339)
	}
	updated := "—"
	if !ws.UpdatedAt.IsZero() {
		updated = ws.UpdatedAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "%s\n", row("Created", created))
	fmt.Fprintf(&b, "%s\n", row("Updated", updated))
	return b.String()
}
