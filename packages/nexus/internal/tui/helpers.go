package tui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/tui/pty"
)

// tabBarOffset returns the number of extra lines consumed by the tab bar
// (1 when tabs are present, 0 otherwise).
func (m Model) tabBarOffset() int {
	if len(m.tabs) == 0 {
		return 0
	}
	return 1
}

// isSplitMode reports whether the terminal is wide enough for the split-pane
// layout (left workspace list + right interactive PTY).
func (m Model) isSplitMode() bool {
	return m.width >= 90
}

// isThreePaneMode reports whether the terminal is wide enough for the
// three-pane layout (workspace list | PTY | spotlight sidebar).
func (m Model) isThreePaneMode() bool {
	return m.width >= 110
}

// paneDimensions returns (leftW, centerW, rightW) for the current layout.
//
//   - Three-pane (>= 110 cols): leftW=28%, rightW=22%, centerW≈50% (remainder minus 1 sep).
//     The left pane has a RoundedBorder; right pane has a RoundedBorder; a
//     single separator column sits between center and right pane.
//   - Two-pane   (>= 90 cols):  leftW=28%, centerW=remainder.
//     The left pane has a RoundedBorder; center pane has a left border only.
//   - Single-pane:              leftW=centerW=full inner, rightW=0.
//
// The widths are outer pane dimensions (including any border chars). Content
// inside a bordered pane is always 2 cols narrower (1 left + 1 right border).
func (m Model) paneDimensions() (leftW, centerW, rightW int) {
	inner := max(m.width-4, 24)
	if m.isThreePaneMode() {
		leftW = int(float64(inner) * 0.28)
		rightW = int(float64(inner) * 0.22)
		centerW = max(inner-leftW-rightW-1, 20) // 1 sep column between center and right; ≈50%
		return
	}
	if m.isSplitMode() {
		leftW = int(float64(inner) * 0.28)
		centerW = max(inner-leftW, 20) // no explicit sep; left border on center pane
		rightW = 0
		return
	}
	leftW = inner
	centerW = inner
	rightW = 0
	return
}

// leftPaneWidth returns the width the workspace list should occupy.
func (m Model) leftPaneWidth() int {
	leftW, _, _ := m.paneDimensions()
	return leftW
}

// ptyPaneDimensions returns the (cols, rows) the PTY center pane should use.
// In two-pane mode the center pane has a 1-col left border, so the PTY content
// is 1 col narrower than centerW. In three-pane mode there is no left border.
func (m Model) ptyPaneDimensions() (cols, rows int) {
	_, centerW, _ := m.paneDimensions()
	cols = centerW
	if m.isSplitMode() && !m.isThreePaneMode() {
		cols = max(centerW-1, 4) // left border on center pane in two-pane
	}
	rows = max(m.height-7-2*m.tabBarOffset(), 4)
	return cols, rows
}

// paneAtX returns which layout pane contains terminal column x.
// x is an absolute terminal column (0-indexed).
func (m Model) paneAtX(x int) layoutPane {
	if !m.isSplitMode() {
		return layoutPaneNone
	}
	// Subtract the 2-column outer padding to get an inner-relative index.
	adj := x - 2
	if adj < 0 {
		return layoutPaneNone
	}
	if adj <= m.leftPaneRight {
		return layoutPaneLeft
	}
	if !m.isThreePaneMode() {
		return layoutPaneCenter
	}
	if adj <= m.centerPaneRight {
		return layoutPaneCenter
	}
	return layoutPaneRight
}

// selectedWorkspace returns the workspace currently highlighted in the list,
// or nil if there is none.
func (m Model) selectedWorkspace() *workspace.Workspace {
	it := m.list.SelectedItem()
	if it == nil {
		return nil
	}
	wi, ok := it.(workspaceItem)
	if !ok {
		return nil
	}
	return &wi.w
}

// maybeSwitchPTY opens or closes the right-pane PTY session based on the
// currently selected workspace. It is a no-op when the selection or the
// running state hasn't changed. Must be called from Update (not View).
func (m Model) maybeSwitchPTY() (Model, tea.Cmd) {
	if !m.isSplitMode() {
		return m, nil
	}

	// Determine which workspace we want a PTY for.
	selWS := m.selectedWorkspace()
	var desiredWsID string
	if selWS != nil && selWS.State == workspace.StateRunning {
		desiredWsID = selWS.ID
	}

	// No change needed.
	if m.ptyWsID == desiredWsID {
		return m, nil
	}

	// Close the old PTY subscription (channel closes → listener goroutine exits).
	if m.cancelPTY != nil {
		m.cancelPTY()
		m.cancelPTY = nil
	}
	// Ask the daemon to close the old session (best-effort, non-blocking).
	if m.mux != nil && m.ptyPane != nil && m.ptyPane.SessionID() != "" {
		mux := m.mux
		sid := m.ptyPane.SessionID()
		go func() { _ = mux.Send("pty.close", map[string]any{"sessionId": sid}) }()
	}
	prevFocused := m.ptyFocused
	m.ptyPane = nil
	m.ptyDataCh = nil
	m.ptyFocused = false
	m.ptyWsID = desiredWsID // set now to prevent duplicate opens on the next tick

	if desiredWsID == "" || m.mux == nil {
		return m, ptyMouseModeCmd(prevFocused, false)
	}
	cols, rows := m.ptyPaneDimensions()
	return m, tea.Batch(ptyMouseModeCmd(prevFocused, false), pty.OpenPTYCmd(m.mux, desiredWsID, cols, rows))
}

// updateListSize recalculates listHeight from terminal dimensions and calls
// SetSize on the bubbles list. Must be called whenever m.height or m.tabs
// changes.
//
// Layout: 2 header lines + 2*tabBarOffset tab rows + 2 spacers + 3 footer
// lines = 7 + 2*tabBarOffset lines of fixed overhead. The list occupies the
// remaining body height. In split mode the left pane has a RoundedBorder so
// the inner content area is 2 cols and 2 rows smaller.
func (m *Model) updateListSize() {
	if m.height == 0 {
		return
	}
	h := max(m.height-7-2*m.tabBarOffset(), 6)
	m.listHeight = h
	w := m.listWidth
	if m.isSplitMode() {
		// RoundedBorder on left pane: 1 col each side, 1 row top/bottom.
		w = max(w-2, 4)
		h = max(h-2, 4)
	}
	m.list.SetSize(w, h)
}

func (m Model) blocksOverlayInput() bool {
	if m.showNoProfile {
		return true
	}
	if m.showWizard {
		return true
	}
	if m.showHelp {
		return true
	}
	if m.panel != panelNone {
		return true
	}
	if m.prompt != promptNone {
		return true
	}
	if m.createMode {
		return true
	}
	if m.confirmDelete {
		return true
	}
	return false
}

// selectedWorkspaceID extracts the workspace ID from a list item.
func selectedWorkspaceID(it list.Item) string {
	if it == nil {
		return ""
	}
	wi, ok := it.(workspaceItem)
	if !ok {
		return ""
	}
	return wi.w.ID
}

// reselectByID selects the list item whose workspace ID matches id.
func reselectByID(l *list.Model, id string) {
	items := l.Items()
	for i, it := range items {
		wi, ok := it.(workspaceItem)
		if ok && wi.w.ID == id {
			l.Select(i)
			return
		}
	}
}
