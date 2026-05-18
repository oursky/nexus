package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/pty"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.mouseSupportEnabled = true
		m.listWidth = m.leftPaneWidth()
		m.updateListSize()

		// Update pane x-boundaries for mouse hit-testing.
		leftW, centerW, _ := m.paneDimensions()
		m.leftPaneRight = leftW - 1
		// Three-pane: centerW does not include sep; right pane starts after centerW.
		// Two-pane: centerW includes all non-left space; center goes to inner-1.
		m.centerPaneRight = leftW + centerW - 1

		// Resize the PTY pane if visible and send pty.resize to daemon.
		var resizeCmd tea.Cmd
		if m.ptyPane != nil && m.mux != nil {
			cols, rows := m.ptyPaneDimensions()
			m.ptyPane.Resize(cols, rows)
			resizeCmd = m.ptyPane.ResizeCmd(m.mux)
		}
		// Re-evaluate PTY visibility after size change.
		m, ptyCmd := m.maybeSwitchPTY()
		return m, tea.Batch(resizeCmd, ptyCmd)

	case messages.ConnReady:
		m.mux = msg.Mux
		m.daemonOK = true
		m.statusLine = ""
		return m, tea.Batch(
			m.refreshCmd(),
			tickRefresh(),
			sidebarSpotTickCmd(),
		)

	case messages.ConnErr:
		return m.handleConnErrMsg(msg)

	case localCheckMsg:
		m.noProfileChecking = false
		if msg.alive {
			// Daemon is already running locally — auto-save profile and connect.
			m.statusLine = "connecting…"
			return m, saveLocalProfileCmd(m.localPort)
		}
		// Nothing running — show the menu.
		return m, nil

	case daemonStartDoneMsg:
		m.noProfileBusy = false
		if msg.err != nil {
			m.noProfileErr = msg.err.Error()
			return m, nil
		}
		// Daemon started — save profile and connect.
		m.statusLine = "connecting…"
		return m, saveLocalProfileCmd(m.localPort)

	case noProfileSpinTickMsg:
		m.noProfileSpinIdx++
		if m.noProfileBusy || m.noProfileChecking {
			return m, noProfileSpinTick()
		}
		return m, nil

	case wizardSavedMsg:
		m.showWizard = false
		m.showNoProfile = false
		m.noProfileBusy = false
		m.wizardBusy = false
		m.statusLine = "connecting…"
		return m, openDaemonConn()

	case wizardErrMsg:
		m.wizardBusy = false
		m.wizardErr = msg.err.Error()
		if m.showNoProfile {
			// Error occurred during local profile save triggered from no-profile flow.
			m.noProfileBusy = false
			m.noProfileErr = msg.err.Error()
		}
		return m, nil

	case messages.Workspaces:
		return m.handleWorkspacesMsg(msg)

	// PTY split-pane messages.
	case pty.PtyOpenedMsg:
		return m.handlePtyOpenedMsg(msg)

	case pty.PtyDataMsg:
		return m.handlePtyDataMsg(msg)

	case pty.PtyClosedMsg:
		return m.handlePtyClosedMsg(msg)

	case pty.PtyErrMsg:
		if msg.Err != nil {
			m.statusLine = "PTY: " + msg.Err.Error()
		}
		m.ptyWsID = "" // allow retry on next refresh
		return m, nil

	case messages.WorkspacesErr:
		m.daemonOK = false
		m.statusLine = msg.Err.Error()
		return m, nil

	case messages.RefreshTick:
		if m.quitting {
			return m, nil
		}
		if m.mux != nil && !m.blocksOverlayInput() {
			return m, m.refreshCmd()
		}
		return m, nil

	case messages.DetailLoaded:
		w := msg.Ws
		m.detail = &w
		m.statusLine = ""
		return m, nil

	case messages.DetailErr:
		m.statusLine = msg.Err.Error()
		return m, nil

	case messages.MutationDone:
		return m.handleMutationDoneMsg(msg)

	case messages.SpotlightsData:
		if msg.Err != nil {
			m.statusLine = msg.Err.Error()
			return m, nil
		}
		m.spotForwards = msg.Forwards
		if m.spotSel >= len(m.spotForwards) {
			m.spotSel = max(0, len(m.spotForwards)-1)
		}
		return m, nil

	case messages.SyncListData:
		if msg.Err != nil {
			m.statusLine = msg.Err.Error()
			return m, nil
		}
		m.syncRows = msg.Rows
		if m.syncSel >= len(m.syncRows) {
			m.syncSel = max(0, len(m.syncRows)-1)
		}
		return m, nil

	case messages.CreateDone:
		m.createMode = false
		m.createStep = 0
		if msg.Err != nil {
			m.statusLine = msg.Err.Error()
			return m, nil
		}
		m.statusLine = "workspace created"
		return m, m.refreshCmd()

	case messages.ForkDone:
		return m.handleForkDoneMsg(msg)

	case messages.ShellReturned:
		return m.handleShellReturnedMsg(msg)

	case messages.SidebarSpot:
		return m.handleSidebarSpotMsg(msg)

	case messages.SidebarDiscovered:
		if msg.Err == nil {
			m.sidebarDiscovered = msg.Ports
		}
		return m, nil

	case messages.TunnelStarted:
		return m.handleTunnelStartedMsg(msg)

	case messages.SidebarSpotTick:
		if m.quitting {
			return m, nil
		}
		var cmds []tea.Cmd
		if m.isThreePaneMode() && m.mux != nil && !m.blocksOverlayInput() {
			cmds = append(cmds, m.loadSidebarSpotCmd())
			cmds = append(cmds, m.loadSidebarDiscoveredCmd())
		}
		cmds = append(cmds, sidebarSpotTickCmd())
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.showWizard {
		var cmd tea.Cmd
		switch m.wizardStep {
		case 0:
			m.wizardHostTI, cmd = m.wizardHostTI.Update(msg)
		case 1:
			m.wizardPortTI, cmd = m.wizardPortTI.Update(msg)
		case 2:
			m.wizardKeyTI, cmd = m.wizardKeyTI.Update(msg)
		}
		return m, cmd
	}

	if m.createMode {
		var cmd tea.Cmd
		switch m.createStep {
		case 0:
			m.nameTI, cmd = m.nameTI.Update(msg)
		case 1:
			m.repoTI, cmd = m.repoTI.Update(msg)
		case 2:
			m.refTI, cmd = m.refTI.Update(msg)
		}
		return m, cmd
	}
	if m.prompt != promptNone {
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// sendMouseToPTY encodes a BubbleTea mouse event as a VT200 mouse escape
// sequence and forwards it to the active PTY via the pty.write RPC.
// It is a no-op unless the program inside the PTY has explicitly requested
// mouse-reporting mode; forwarding to a plain shell prompt causes gibberish.
func (m Model) sendMouseToPTY(msg tea.MouseMsg) tea.Cmd {
	if m.ptyPane == nil || m.mux == nil {
		return nil
	}
	if !m.ptyPane.MouseEnabled() {
		return nil
	}
	leftW, _, _ := m.paneDimensions()
	// PTY view origin (absolute terminal coordinates, 0-indexed):
	//   X: outer padding (2) + left pane width including border (leftW)
	//      + 1 for the center pane's left border (two-pane) or 0 (three-pane)
	//   Y: 2 header lines + 1 blank line + 2*tabBarOffset tab lines
	ptyX := 2 + leftW
	if !m.isThreePaneMode() {
		ptyX++ // center pane has left border in two-pane layout
	}
	ptyY := 3 + 2*m.tabBarOffset()

	// PTY-relative, 1-indexed.
	px := msg.X - ptyX + 1
	py := msg.Y - ptyY + 1
	if px < 1 {
		px = 1
	}
	if py < 1 {
		py = 1
	}
	// VT200 X10 mouse: coordinates encoded as char+32, max 223.
	if px > 223 {
		px = 223
	}
	if py > 223 {
		py = 223
	}

	var btn int
	switch msg.Button {
	case tea.MouseButtonLeft:
		btn = 0
		if msg.Action == tea.MouseActionRelease {
			btn = 3
		}
	case tea.MouseButtonMiddle:
		btn = 1
	case tea.MouseButtonRight:
		btn = 2
	case tea.MouseButtonWheelUp:
		btn = 64
	case tea.MouseButtonWheelDown:
		btn = 65
	default:
		return nil
	}

	data := fmt.Sprintf("\x1b[M%c%c%c", rune(btn+32), rune(px+32), rune(py+32))
	sessionID := m.ptyPane.SessionID()
	mux := m.mux
	return func() tea.Msg {
		_ = mux.Send("pty.write", map[string]any{
			"sessionId": sessionID,
			"data":      data,
		})
		return nil
	}
}

// handleMouse handles tea.MouseMsg events for click-to-focus, scroll, and PTY
// mouse forwarding.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.blocksOverlayInput() || !m.mouseSupportEnabled {
		return m, nil
	}

	prevPtyFocused := m.ptyFocused
	pane := m.paneAtX(msg.X)

	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		switch pane {
		case layoutPaneLeft:
			var listCmd tea.Cmd
			if msg.Button == tea.MouseButtonWheelUp {
				m.list, listCmd = m.list.Update(tea.KeyMsg{Type: tea.KeyUp})
			} else {
				m.list, listCmd = m.list.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			m, ptyCmd := m.maybeSwitchPTY()
			return m, tea.Batch(listCmd, ptyCmd)
		case layoutPaneCenter:
			if m.ptyPane != nil {
				return m, m.sendMouseToPTY(msg)
			}
		case layoutPaneRight:
			if msg.Button == tea.MouseButtonWheelUp {
				if m.sidebarSel > 0 {
					m.sidebarSel--
				}
			} else {
				if m.sidebarSel < len(m.sidebarFwds)-1 {
					m.sidebarSel++
				}
			}
		}

	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionRelease {
			if pane == layoutPaneCenter && m.ptyPane != nil {
				return m, m.sendMouseToPTY(msg)
			}
			return m, nil
		}
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		switch pane {
		case layoutPaneLeft:
			m.ptyFocused = false
			m.sidebarFocused = false
			// Click-to-select: map the click's Y coordinate to a list item.
			//
			// Layout rows (0-indexed, absolute):
			//   0            headerTop
			//   1            headerBottom
			//   2..3         (optional) tab-separator + tab-bar (2*tabBarOffset lines)
			//   2+2*T        blank ""
			//   3+2*T        body/list start → RoundedBorder top row
			//   4+2*T        list title ("Workspaces") — 1 line inside border
			//   5+2*T        first list item row (groupedDelegate.Height()=2 lines each)
			//
			// where T = m.tabBarOffset().
			listItemsStartY := 5 + 2*m.tabBarOffset()
			relY := msg.Y - listItemsStartY
			if relY >= 0 && m.list.FilterState() != list.Filtering {
				slot := relY / 2 // groupedDelegate height = 2
				firstVisible := m.list.Paginator.Page * m.list.Paginator.PerPage
				targetIdx := firstVisible + slot
				items := m.list.Items()
				if targetIdx >= 0 && targetIdx < len(items) {
					m.list.Select(targetIdx)
					m, ptyCmd := m.maybeSwitchPTY()
					sideCmd := tea.Batch(m.loadSidebarSpotCmd(), m.loadSidebarDiscoveredCmd())
					return m, tea.Batch(ptyMouseModeCmd(prevPtyFocused, m.ptyFocused), ptyCmd, sideCmd)
				}
			}
		case layoutPaneCenter:
			if !m.ptyFocused {
				m.ptyFocused = true
				m.sidebarFocused = false
			}
			if m.ptyPane != nil {
				return m, tea.Batch(ptyMouseModeCmd(prevPtyFocused, m.ptyFocused), m.sendMouseToPTY(msg))
			}
		case layoutPaneRight:
			m.sidebarFocused = true
			m.ptyFocused = false
			// Best-effort click-to-select for sidebar entries.
			// Sidebar header (inside RoundedBorder):
			//   row 0 (border top)
			//   row 1: "SPOTLIGHT"
			//   row 2: "[a] Toggle all ..."
			//   row 3: "─────────" separator
			//   row 4+: forward entries
			bodyY := 3 + 2*m.tabBarOffset()
			relY := msg.Y - bodyY - 4 // skip border-top + 3 header lines
			if relY >= 0 && relY < len(m.sidebarFwds) {
				m.sidebarSel = relY
			}
		}

	default:
		if pane == layoutPaneCenter && m.ptyPane != nil {
			return m, m.sendMouseToPTY(msg)
		}
	}

	return m, ptyMouseModeCmd(prevPtyFocused, m.ptyFocused)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// When the spotlight sidebar has focus, route keys there.
	if m.sidebarFocused && m.isThreePaneMode() && !m.blocksOverlayInput() {
		return m.handleSidebarKeys(msg)
	}

	// When the PTY right pane has focus, forward all keys to it.
	// ctrl+a detaches from the PTY pane; in three-pane mode it moves focus to
	// the spotlight sidebar instead of the left pane.
	if m.ptyFocused && m.ptyPane != nil && !m.blocksOverlayInput() {
		if msg.String() == "ctrl+a" {
			m.ptyFocused = false
			if m.isThreePaneMode() {
				m.sidebarFocused = true
			}
			return m, tea.EnableMouseAllMotion
		}
		// Forward all other keys (including ctrl+c, q) to the PTY.
		return m, m.ptyPane.SendInputCmd(m.mux, msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		if !m.showWizard && !m.showNoProfile {
			m.quitting = true
			if m.cancelPTY != nil {
				m.cancelPTY()
				m.cancelPTY = nil
			}
			m.closeSidebarTunnel()
			if m.mux != nil {
				m.mux.Close()
				m.mux = nil
			}
			return m, tea.Quit
		}
	}

	if m.showNoProfile {
		return m.handleNoProfileKeys(msg)
	}

	if m.showWizard {
		return m.handleWizardKeys(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		if m.cancelPTY != nil {
			m.cancelPTY()
			m.cancelPTY = nil
		}
		m.closeSidebarTunnel()
		if m.mux != nil {
			m.mux.Close()
			m.mux = nil
		}
		return m, tea.Quit
	}

	if m.showHelp {
		if msg.String() == "esc" || msg.String() == "?" {
			m.showHelp = false
		}
		return m, nil
	}

	if msg.String() == "?" {
		m.showHelp = true
		return m, nil
	}

	// Tab jump: 1–9 selects the Nth session tab in the workspace list.
	if !m.blocksOverlayInput() {
		key := msg.String()
		if len(key) == 1 && key >= "1" && key <= "9" {
			idx := int(key[0]-'0') - 1
			if idx < len(m.tabs) {
				wsID := m.tabs[idx]
				m.activeTabWS = wsID
				m.detail = nil
				m.panel = panelNone
				reselectByID(&m.list, wsID)
			}
			return m, nil
		}
	}

	if m.createMode {
		return m.handleCreateKeys(msg)
	}

	if m.prompt != promptNone {
		return m.handlePromptKeys(msg)
	}

	if m.panel == panelConnect {
		if msg.String() == "esc" || msg.String() == "c" {
			m.panel = panelNone
		}
		return m, nil
	}
	if m.panel == panelSpotlight {
		return m.handleSpotlightKeys(msg)
	}
	if m.panel == panelSync {
		return m.handleSyncKeys(msg)
	}

	if msg.String() == "esc" {
		if m.confirmDelete {
			m.confirmDelete = false
			m.pendingDeleteID = ""
			return m, nil
		}
		if m.detail != nil {
			m.detail = nil
			return m, nil
		}
	}

	if m.confirmDelete {
		switch strings.ToLower(msg.String()) {
		case "y":
			id := m.pendingDeleteID
			m.confirmDelete = false
			m.pendingDeleteID = ""
			if m.detail != nil && m.detail.ID == id {
				m.detail = nil
			}
			return m, m.rpcVoidCmd("workspace.remove", id)
		case "n":
			m.confirmDelete = false
			m.pendingDeleteID = ""
			return m, nil
		default:
			m.confirmDelete = false
			m.pendingDeleteID = ""
			return m, nil
		}
	}

	switch msg.String() {
	case "r":
		return m, m.refreshCmd()
	}

	if m.detail != nil {
		return m.handleDetailKeys(msg)
	}

	return m.handleListKeys(msg)
}

func (m Model) handleCreateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.createMode = false
		m.createStep = 0
		m.nameTI.Blur()
		m.repoTI.Blur()
		m.refTI.Blur()
		return m, nil
	case "tab":
		m.createStep = (m.createStep + 1) % 3
		m, cmd := m.refocusCreateStep()
		return m, cmd
	case "shift+tab":
		m.createStep = (m.createStep + 2) % 3
		m, cmd := m.refocusCreateStep()
		return m, cmd
	case "enter":
		switch m.createStep {
		case 0:
			if strings.TrimSpace(m.nameTI.Value()) == "" {
				m.statusLine = "name is required"
				return m, nil
			}
			m.createStep = 1
			m.nameTI.Blur()
			m.repoTI.Blur()
			return m, m.repoTI.Focus()
		case 1:
			if strings.TrimSpace(m.repoTI.Value()) == "" {
				m.statusLine = "repo is required"
				return m, nil
			}
			m.createStep = 2
			m.repoTI.Blur()
			m.refTI.Blur()
			return m, m.refTI.Focus()
		default:
			spec := workspace.CreateSpec{
				Repo:          strings.TrimSpace(m.repoTI.Value()),
				Ref:           strings.TrimSpace(m.refTI.Value()),
				WorkspaceName: strings.TrimSpace(m.nameTI.Value()),
				AgentProfile:  "default",
				Policy:        workspace.Policy{},
			}
			m.createMode = false
			m.createStep = 0
			m.nameTI.Blur()
			m.repoTI.Blur()
			m.refTI.Blur()
			nt, rt, rf := newCreateInputs()
			m.nameTI, m.repoTI, m.refTI = nt, rt, rf
			return m, m.createWorkspaceCmd(spec)
		}
	default:
		var cmd tea.Cmd
		switch m.createStep {
		case 0:
			m.nameTI, cmd = m.nameTI.Update(msg)
		case 1:
			m.repoTI, cmd = m.repoTI.Update(msg)
		case 2:
			m.refTI, cmd = m.refTI.Update(msg)
		}
		return m, cmd
	}
}

func (m Model) refocusCreateStep() (Model, tea.Cmd) {
	m.nameTI.Blur()
	m.repoTI.Blur()
	m.refTI.Blur()
	switch m.createStep {
	case 0:
		return m, m.nameTI.Focus()
	case 1:
		return m, m.repoTI.Focus()
	default:
		return m, m.refTI.Focus()
	}
}

func (m Model) handlePromptKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.prompt = promptNone
		m.promptInput.SetValue("")
		m.promptInput.Blur()
		m.pendingSyncPath = ""
		return m, nil
	case "enter":
		line := strings.TrimSpace(m.promptInput.Value())
		m.promptInput.SetValue("")
		m.promptInput.Blur()
		switch m.prompt {
		case promptForkChild:
			if line == "" || m.detail == nil {
				m.prompt = promptNone
				return m, nil
			}
			m.prompt = promptNone
			return m, m.forkCmd(m.detail.ID, line)
		case promptSpotPort:
			m.prompt = promptNone
			if m.detail == nil {
				return m, nil
			}
			p, err := strconv.Atoi(line)
			if err != nil || p <= 0 || p > 65535 {
				m.statusLine = "invalid port"
				return m, m.loadSpotlightsCmd(m.detail.ID)
			}
			return m, m.startSpotlightCmd(m.detail.ID, p)
		case promptSidebarSpotPort:
			m.prompt = promptNone
			ws := m.selectedWorkspace()
			if ws == nil {
				return m, nil
			}
			p, err := strconv.Atoi(line)
			if err != nil || p <= 0 || p > 65535 {
				m.statusLine = "invalid port"
				return m, m.loadSidebarSpotCmd()
			}
			return m, m.startSpotlightCmd(ws.ID, p)
		case promptSyncLocal:
			if line == "" {
				m.prompt = promptNone
				return m, nil
			}
			m.pendingSyncPath = line
			m.prompt = promptSyncDir
			m.promptInput.Placeholder = "direction: bidirectional | up | down"
			return m, m.promptInput.Focus()
		case promptSyncDir:
			m.prompt = promptNone
			path := m.pendingSyncPath
			m.pendingSyncPath = ""
			dir := line
			if dir == "" {
				dir = "bidirectional"
			}
			if m.detail == nil {
				return m, nil
			}
			return m, m.syncStartCmd(m.detail.ID, path, dir)
		default:
			m.prompt = promptNone
			return m, nil
		}
	default:
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}
}

func (m Model) handleSpotlightKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "l":
		m.panel = panelNone
		return m, nil
	case "n":
		m.prompt = promptSpotPort
		m.promptInput.Placeholder = "remote port"
		return m, m.promptInput.Focus()
	case "x":
		if m.detail == nil || len(m.spotForwards) == 0 {
			return m, nil
		}
		if m.spotSel < 0 || m.spotSel >= len(m.spotForwards) {
			return m, nil
		}
		f := m.spotForwards[m.spotSel]
		if f == nil {
			return m, nil
		}
		return m, m.removeForwardCmd(m.detail.ID, f.ID)
	case "up", "k":
		if m.spotSel > 0 {
			m.spotSel--
		}
		return m, nil
	case "down", "j":
		if m.spotSel < len(m.spotForwards)-1 {
			m.spotSel++
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleSyncKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "y":
		m.panel = panelNone
		return m, nil
	case "n":
		m.prompt = promptSyncLocal
		m.promptInput.Placeholder = "local path to sync"
		return m, m.promptInput.Focus()
	case "x":
		if m.detail == nil || len(m.syncRows) == 0 {
			return m, nil
		}
		if m.syncSel < 0 || m.syncSel >= len(m.syncRows) {
			return m, nil
		}
		sid := m.syncRows[m.syncSel].SessionID
		return m, m.syncStopSessionCmd(sid)
	case "p":
		if len(m.syncRows) == 0 || m.detail == nil {
			return m, nil
		}
		sid := m.syncRows[m.syncSel].SessionID
		return m, m.syncPauseCmd(sid)
	case "r":
		if len(m.syncRows) == 0 || m.detail == nil {
			return m, nil
		}
		sid := m.syncRows[m.syncSel].SessionID
		return m, m.syncResumeCmd(sid)
	case "up", "k":
		if m.syncSel > 0 {
			m.syncSel--
		}
		return m, nil
	case "down", "j":
		if m.syncSel < len(m.syncRows)-1 {
			m.syncSel++
		}
		return m, nil
	default:
		return m, nil
	}
}

// handleSidebarKeys handles keyboard input when the spotlight sidebar pane has
// focus (three-pane layout only).
func (m Model) handleSidebarKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Row-confirm state for x (remove): only y/n/esc are handled.
	if m.sidebarConfirmRemove {
		switch msg.String() {
		case "y":
			m.sidebarConfirmRemove = false
			ws := m.selectedWorkspace()
			if ws == nil || m.sidebarSel < 0 || m.sidebarSel >= len(m.sidebarFwds) {
				return m, nil
			}
			f := m.sidebarFwds[m.sidebarSel]
			if f == nil {
				return m, nil
			}
			return m, m.removeForwardCmd(ws.ID, f.ID)
		default:
			// n, esc, x, or any other key cancels.
			m.sidebarConfirmRemove = false
			return m, nil
		}
	}

	switch msg.String() {
	case "tab", "esc":
		m.sidebarFocused = false
		return m, nil
	case "a":
		// Toggle all: if any forwards exist, stop all; otherwise start all discovered.
		ws := m.selectedWorkspace()
		if ws == nil {
			return m, nil
		}
		if len(m.sidebarFwds) > 0 {
			// Stopping all — close the client-side SSH tunnel immediately.
			m.closeSidebarTunnel()
			m.sidebarTunnelErr = ""
			return m, m.stopWorkspaceSpotCmd(ws.ID)
		}
		return m, m.startAllDiscoveredCmd(ws.ID)
	case "n":
		ws := m.selectedWorkspace()
		if ws == nil {
			return m, nil
		}
		m.prompt = promptSidebarSpotPort
		m.promptInput.Placeholder = "remote port"
		m.promptInput.SetValue("")
		return m, m.promptInput.Focus()
	case "x", "delete":
		if len(m.sidebarFwds) == 0 {
			return m, nil
		}
		if m.sidebarSel < 0 || m.sidebarSel >= len(m.sidebarFwds) {
			return m, nil
		}
		m.sidebarConfirmRemove = true
		return m, nil
	case "up", "k":
		if m.sidebarSel > 0 {
			m.sidebarSel--
		}
		return m, nil
	case "down", "j":
		if m.sidebarSel < len(m.sidebarFwds)-1 {
			m.sidebarSel++
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleDetailKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		m.detail = nil
		return m, nil
	}
	id := m.detail.ID
	switch msg.String() {
	case "r":
		return m, m.refreshCmd()
	case "s":
		return m, m.rpcVoidCmd("workspace.start", id)
	case "x":
		return m, m.rpcVoidCmd("workspace.stop", id)
	case "d":
		m.confirmDelete = true
		m.pendingDeleteID = id
		return m, nil
	case "c":
		m.panel = panelConnect
		return m, nil
	case "l":
		m.panel = panelSpotlight
		m.spotSel = 0
		return m, m.loadSpotlightsCmd(id)
	case "y":
		m.panel = panelSync
		m.syncSel = 0
		return m, m.loadSyncListCmd(id)
	case "t":
		return m.runShellExec(id)
	case "f":
		m.prompt = promptForkChild
		m.promptInput.Placeholder = "child workspace name"
		return m, m.promptInput.Focus()
	}
	return m, nil
}

// shellExecCmd returns a tea.Cmd that runs nexus workspace shell <wsID> via
// ExecProcess. The TUI resumes when the shell exits, delivering shellReturnedMsg.
func (m Model) runShellExec(wsID string) (tea.Model, tea.Cmd) {
	m.activeTabWS = wsID
	// Pre-register tab so it appears immediately after ExecProcess returns.
	before := len(m.tabs)
	m.tabs = addTab(m.tabs, wsID)
	if len(m.tabs) != before {
		m.updateListSize()
	}
	saveSessionState(sessionState{Tabs: m.tabs, Active: wsID})
	return m, m.shellExecCmd(wsID)
}

func (m Model) handleListKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// l key in three-pane mode focuses the spotlight sidebar directly.
	if m.isThreePaneMode() && msg.String() == "l" {
		m.sidebarFocused = true
		m.ptyFocused = false
		return m, nil
	}

	// In split mode, Tab transfers focus: left → PTY → (three-pane: sidebar).
	if m.isSplitMode() && msg.String() == "tab" {
		prevPtyFocused := m.ptyFocused
		if m.ptyPane != nil {
			m.ptyFocused = true
		} else if m.isThreePaneMode() {
			m.sidebarFocused = true
		}
		return m, ptyMouseModeCmd(prevPtyFocused, m.ptyFocused)
	}

	if msg.String() == "n" || msg.String() == "+" {
		m.createMode = true
		m.createStep = 0
		nt, rt, rf := newCreateInputs()
		m.nameTI, m.repoTI, m.refTI = nt, rt, rf
		return m, m.nameTI.Focus()
	}

	if msg.String() == "t" {
		it := m.list.SelectedItem()
		if it == nil {
			return m, nil
		}
		wi, ok := it.(workspaceItem)
		if !ok {
			return m, nil
		}
		return m.runShellExec(wi.w.ID)
	}

	switch msg.String() {
	case "enter":
		it := m.list.SelectedItem()
		if it == nil {
			return m, nil
		}
		wi, ok := it.(workspaceItem)
		if !ok {
			return m, nil
		}
		return m, m.loadDetailCmd(wi.w.ID)

	case "s", "x", "d":
		it := m.list.SelectedItem()
		if it == nil {
			return m, nil
		}
		wi, ok := it.(workspaceItem)
		if !ok {
			return m, nil
		}
		wid := wi.w.ID
		switch msg.String() {
		case "s":
			return m, m.rpcVoidCmd("workspace.start", wid)
		case "x":
			return m, m.rpcVoidCmd("workspace.stop", wid)
		case "d":
			m.confirmDelete = true
			m.pendingDeleteID = wid
			return m, nil
		}
	}

	var listCmd tea.Cmd
	m.list, listCmd = m.list.Update(msg)
	// After any navigation, re-evaluate which PTY session to show.
	m, ptyCmd := m.maybeSwitchPTY()
	return m, tea.Batch(listCmd, ptyCmd)
}

// handleWorkspacesMsg handles the Workspaces message from the daemon.
func (m Model) handleWorkspacesMsg(msg messages.Workspaces) (Model, tea.Cmd) {
	m.daemonOK = true
	if msg.ProjectsByID != nil {
		m.projectsByID = msg.ProjectsByID
	}
	sel := selectedWorkspaceID(m.list.SelectedItem())
	m.list.SetItems(workspacesToItems(msg.Workspaces, m.projectsByID))
	if sel != "" {
		reselectByID(&m.list, sel)
	}
	if m.detail != nil {
		id := m.detail.ID
		for _, w := range msg.Workspaces {
			if w.ID == id {
				wcopy := w
				m.detail = &wcopy
				break
			}
		}
	}
	// Auto-attach to last active workspace on first load (--auto-attach only).
	if m.autoAttach && !m.autoAttachDone && m.activeTabWS != "" {
		m.autoAttachDone = true
		for _, w := range msg.Workspaces {
			if w.ID == m.activeTabWS && w.State == workspace.StateRunning {
				return m, m.shellExecCmd(m.activeTabWS)
			}
		}
	}
	m, ptyCmd := m.maybeSwitchPTY()
	var sideCmd tea.Cmd
	if m.isThreePaneMode() && m.mux != nil {
		sideCmd = tea.Batch(m.loadSidebarSpotCmd(), m.loadSidebarDiscoveredCmd())
	}
	return m, tea.Batch(ptyCmd, sideCmd)
}

// handleSidebarSpotMsg handles the SidebarSpot message from the daemon.
func (m Model) handleSidebarSpotMsg(msg messages.SidebarSpot) (Model, tea.Cmd) {
	if msg.Err != nil {
		return m, nil
	}
	m.sidebarFwds = msg.Forwards
	if m.sidebarSel >= len(m.sidebarFwds) {
		m.sidebarSel = max(0, len(m.sidebarFwds)-1)
	}

	selWS := m.selectedWorkspace()
	selWsID := ""
	if selWS != nil {
		selWsID = selWS.ID
	}

	// Stop the tunnel if the workspace changed.
	if m.sidebarTunnelWsID != "" && m.sidebarTunnelWsID != selWsID {
		m.closeSidebarTunnel()
	}

	// Count active forwards for the current workspace.
	activeCount := 0
	for _, f := range m.sidebarFwds {
		if f != nil && f.State == spotlight.ForwardStateActive {
			activeCount++
		}
	}

	var tunnelCmd tea.Cmd
	if activeCount > 0 && selWsID != "" {
		// Tunnel needs (re)start if: never started, SSH process died, or
		// a local proxy was expected but is no longer present.
		tunnelNeedsStart := !m.sidebarTunnelLive ||
			(m.sidebarTunnel != nil && m.sidebarTunnel.PID() <= 0)
		if tunnelNeedsStart {
			// Close any dead tunnel or proxy handle before starting a fresh one.
			if m.sidebarTunnel != nil {
				_ = m.sidebarTunnel.Close()
				m.sidebarTunnel = nil
			}
			if m.sidebarLocalProxy != nil {
				_ = m.sidebarLocalProxy.Close()
				m.sidebarLocalProxy = nil
			}
			m.sidebarTunnelLive = false
			m.sidebarTunnelWsID = selWsID
			tunnelCmd = startSidebarTunnelCmd(selWsID, m.sidebarFwds)
		}
	} else if activeCount == 0 {
		m.closeSidebarTunnel()
	}

	return m, tunnelCmd
}

// handleTunnelStartedMsg handles the TunnelStarted message.
func (m Model) handleTunnelStartedMsg(msg messages.TunnelStarted) (Model, tea.Cmd) {
	if msg.WsID != m.sidebarTunnelWsID {
		// Workspace changed while tunnel was starting; discard and close.
		if msg.Tunnel != nil {
			_ = msg.Tunnel.Close()
		}
		if msg.LocalProxy != nil {
			_ = msg.LocalProxy.Close()
		}
		return m, nil
	}
	if msg.Err != nil {
		m.statusLine = "spotlight tunnel: " + msg.Err.Error()
		m.sidebarTunnelLive = false
		m.sidebarTunnelErr = msg.Err.Error()
		return m, nil
	}
	m.sidebarTunnelErr = "" // clear on success
	if msg.Local {
		// Local daemon path. Close any previous local proxy before storing new one.
		if m.sidebarLocalProxy != nil {
			_ = m.sidebarLocalProxy.Close()
			m.sidebarLocalProxy = nil
		}
		if msg.LocalProxy != nil {
			// libkrun VM workspace: local proxy bound on user-facing ports.
			m.sidebarLocalProxy = msg.LocalProxy
		}
		m.sidebarTunnelLive = true
		m.sidebarTunnelLocal = true
		return m, nil
	}
	if msg.Tunnel == nil {
		// No active forwards to tunnel; leave live=false.
		return m, nil
	}
	// New SSH tunnel is up; replace any existing one.
	if m.sidebarTunnel != nil {
		_ = m.sidebarTunnel.Close()
	}
	m.sidebarTunnel = msg.Tunnel
	m.sidebarTunnelLive = true
	m.sidebarTunnelLocal = false
	return m, nil
}

// handleConnErrMsg handles connection errors from the daemon.
func (m Model) handleConnErrMsg(msg messages.ConnErr) (Model, tea.Cmd) {
	m.daemonOK = false
	errStr := msg.Err.Error()
	if strings.Contains(errStr, "no daemon profile configured") {
		// First-run: no profile at all — check if a local daemon is already
		// running before showing the no-profile menu.
		m.showNoProfile = true
		m.noProfileChecking = true
		m.noProfileSel = 0
		m.noProfileErr = ""
		return m, tea.Batch(checkLocalDaemonCmd(m.localPort), noProfileSpinTick())
	}
	if strings.Contains(errStr, "connect to local daemon") && strings.Contains(errStr, "connection refused") {
		// A localhost profile exists but the daemon isn't running.
		// Treat exactly like no-profile: show the start/connect menu.
		m.showNoProfile = true
		m.noProfileChecking = false
		m.noProfileBusy = false
		m.noProfileSel = 0
		m.noProfileErr = ""
		return m, nil
	}
	m.statusLine = errStr
	return m, nil
}

// handlePtyOpenedMsg handles the PTY opened message.
func (m Model) handlePtyOpenedMsg(msg pty.PtyOpenedMsg) (Model, tea.Cmd) {
	if msg.WsID != m.ptyWsID {
		// Selection changed while pty.create was in-flight; discard.
		msg.CancelFn()
		if m.mux != nil {
			mux := m.mux
			sid := msg.SessionID
			go func() { _ = mux.Send("pty.close", map[string]any{"sessionId": sid}) }()
		}
		return m, nil
	}
	cols, rows := m.ptyPaneDimensions()
	m.ptyPane = pty.NewPtyPane(msg.WsID, msg.SessionID, cols, rows)
	m.ptyDataCh = msg.DataCh
	m.cancelPTY = msg.CancelFn
	return m, pty.ListenPTYCmd(msg.DataCh, msg.SessionID)
}

// handlePtyDataMsg handles PTY data messages.
func (m Model) handlePtyDataMsg(msg pty.PtyDataMsg) (Model, tea.Cmd) {
	if m.ptyPane != nil && msg.SessionID == m.ptyPane.SessionID() {
		m.ptyPane.Write(msg.Data)
	}
	if m.ptyDataCh != nil {
		return m, pty.ListenPTYCmd(m.ptyDataCh, msg.SessionID)
	}
	return m, nil
}

// handlePtyClosedMsg handles PTY closed messages.
func (m Model) handlePtyClosedMsg(msg pty.PtyClosedMsg) (Model, tea.Cmd) {
	if m.ptyPane != nil && msg.SessionID == m.ptyPane.SessionID() {
		prevFocused := m.ptyFocused
		m.ptyPane = nil
		m.ptyWsID = ""
		m.cancelPTY = nil
		m.ptyDataCh = nil
		m.ptyFocused = false
		return m, ptyMouseModeCmd(prevFocused, false)
	}
	return m, nil
}

// handleMutationDoneMsg handles mutation completion messages.
func (m Model) handleMutationDoneMsg(msg messages.MutationDone) (Model, tea.Cmd) {
	if msg.Err != nil {
		m.statusLine = msg.Err.Error()
		return m, nil
	}
	m.statusLine = ""
	if m.panel == panelSpotlight && m.detail != nil {
		return m, m.loadSpotlightsCmd(m.detail.ID)
	}
	if m.panel == panelSync && m.detail != nil {
		return m, m.loadSyncListCmd(m.detail.ID)
	}
	cmds := []tea.Cmd{m.refreshCmd()}
	if m.isThreePaneMode() && m.mux != nil {
		cmds = append(cmds, m.loadSidebarSpotCmd())
	}
	return m, tea.Batch(cmds...)
}

// handleForkDoneMsg handles fork completion messages.
func (m Model) handleForkDoneMsg(msg messages.ForkDone) (Model, tea.Cmd) {
	m.prompt = promptNone
	m.promptInput.Blur()
	if msg.Err != nil {
		m.statusLine = msg.Err.Error()
		return m, nil
	}
	m.statusLine = "fork created"
	pid := ""
	if m.detail != nil {
		pid = m.detail.ID
	}
	if pid != "" {
		return m, tea.Batch(m.refreshCmd(), m.loadDetailCmd(pid))
	}
	return m, m.refreshCmd()
}

// handleShellReturnedMsg handles shell execution completion messages.
func (m Model) handleShellReturnedMsg(msg messages.ShellReturned) (Model, tea.Cmd) {
	if msg.Err != nil && msg.Err.Error() != "" && msg.Err.Error() != "exit status 0" {
		// ExecProcess may pass non-nil for exit code !=0; still refresh.
		m.statusLine = msg.Err.Error()
	}
	// Record in session tabs (addTab is idempotent).
	if msg.WsID != "" {
		m.tabs = addTab(m.tabs, msg.WsID)
		m.activeTabWS = msg.WsID
		m.updateListSize()
		saveSessionState(sessionState{Tabs: m.tabs, Active: msg.WsID})
	}
	return m, m.refreshCmd()
}
