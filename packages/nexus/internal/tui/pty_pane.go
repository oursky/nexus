package tui

import (
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
)

// mouseModeEnableSeqs are DEC private mode sequences that enable mouse
// reporting in a terminal. When a program inside the PTY sends one of these,
// it means it wants to receive mouse events.
var mouseModeEnableSeqs = []string{
	"\x1b[?1000h", // VT200 X10 — button press only
	"\x1b[?1002h", // button-event tracking
	"\x1b[?1003h", // any-event tracking
	"\x1b[?1006h", // SGR extended mouse
}

// mouseModeDisableSeqs are the corresponding disable sequences.
var mouseModeDisableSeqs = []string{
	"\x1b[?1000l",
	"\x1b[?1002l",
	"\x1b[?1003l",
	"\x1b[?1006l",
}

// PtyPane manages an interactive PTY session rendered as a VT100 terminal
// inside a Bubble Tea view. It feeds incoming pty.data bytes into a
// charmbracelet/x/vt SafeEmulator and exposes Render() for the split-pane
// View.
type PtyPane struct {
	width     int
	height    int
	sessionID string
	wsID      string
	term      *vt.SafeEmulator

	// ptyMouseEnabled is true when the program inside the PTY has requested
	// mouse reporting (via DEC private mode ?1000/?1002/?1003/?1006). Only
	// forward mouse events as VT sequences when this is set; otherwise clicks
	// are used only for TUI pane-focus navigation.
	ptyMouseEnabled bool
}

// NewPtyPane creates a PtyPane for the given workspace and session.
func NewPtyPane(wsID, sessionID string, width, height int) *PtyPane {
	return &PtyPane{
		width:     width,
		height:    height,
		sessionID: sessionID,
		wsID:      wsID,
		term:      vt.NewSafeEmulator(width, height),
	}
}

// Write feeds raw PTY output bytes into the VT100 emulator and tracks whether
// the program inside the PTY has requested mouse-reporting mode.
func (p *PtyPane) Write(data string) {
	for _, seq := range mouseModeEnableSeqs {
		if strings.Contains(data, seq) {
			p.ptyMouseEnabled = true
			break
		}
	}
	for _, seq := range mouseModeDisableSeqs {
		if strings.Contains(data, seq) {
			// Only clear if no enable sequence also appears in the same chunk.
			hasEnable := false
			for _, en := range mouseModeEnableSeqs {
				if strings.Contains(data, en) {
					hasEnable = true
					break
				}
			}
			if !hasEnable {
				p.ptyMouseEnabled = false
			}
			break
		}
	}
	_, _ = p.term.Write([]byte(data))
}

// MouseEnabled reports whether the program inside the PTY has requested
// mouse-reporting mode. When false, mouse click events must not be forwarded
// as VT escape sequences (doing so would paste gibberish into the shell).
func (p *PtyPane) MouseEnabled() bool {
	return p.ptyMouseEnabled
}

// Render returns the current terminal screen as an ANSI-encoded string
// suitable for embedding directly in a BubbleTea View string.
func (p *PtyPane) Render() string {
	return p.term.Render()
}

// Resize updates the VT emulator's grid dimensions.
func (p *PtyPane) Resize(width, height int) {
	if p.width == width && p.height == height {
		return
	}
	p.width = width
	p.height = height
	p.term.Resize(width, height)
}

// sendInputCmd returns a tea.Cmd that forwards a BubbleTea key event as raw
// bytes to the PTY via the pty.write RPC.
func (p *PtyPane) sendInputCmd(mux *rpc.MuxConn, msg tea.KeyMsg) tea.Cmd {
	data := keyMsgToBytes(msg)
	if data == "" {
		return nil
	}
	sessionID := p.sessionID
	return func() tea.Msg {
		_ = mux.Send("pty.write", map[string]any{
			"sessionId": sessionID,
			"data":      data,
		})
		return nil
	}
}

// resizeCmd returns a tea.Cmd that sends a pty.resize notification to the
// daemon with the pane's current dimensions.
func (p *PtyPane) resizeCmd(mux *rpc.MuxConn) tea.Cmd {
	sessionID := p.sessionID
	cols, rows := p.width, p.height
	return func() tea.Msg {
		_ = mux.Send("pty.resize", map[string]any{
			"sessionId": sessionID,
			"cols":      cols,
			"rows":      rows,
		})
		return nil
	}
}

// keyMsgToBytes converts a BubbleTea KeyMsg to the ANSI byte sequence the PTY
// expects. Returns an empty string for unrecognised keys.
func keyMsgToBytes(msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyRunes:
		if msg.Alt {
			return "\x1b" + string(msg.Runes)
		}
		return string(msg.Runes)
	case tea.KeySpace:
		if msg.Alt {
			return "\x1b "
		}
		return " "
	case tea.KeyEnter:
		return "\r"
	case tea.KeyBackspace:
		return "\x7f"
	case tea.KeyTab:
		return "\t"
	case tea.KeyEscape:
		return "\x1b"
	case tea.KeyUp:
		if msg.Alt {
			return "\x1b\x1b[A"
		}
		return "\x1b[A"
	case tea.KeyDown:
		if msg.Alt {
			return "\x1b\x1b[B"
		}
		return "\x1b[B"
	case tea.KeyRight:
		if msg.Alt {
			return "\x1b\x1b[C"
		}
		return "\x1b[C"
	case tea.KeyLeft:
		if msg.Alt {
			return "\x1b\x1b[D"
		}
		return "\x1b[D"
	case tea.KeyHome:
		return "\x1b[H"
	case tea.KeyEnd:
		return "\x1b[F"
	case tea.KeyPgUp:
		return "\x1b[5~"
	case tea.KeyPgDown:
		return "\x1b[6~"
	case tea.KeyInsert:
		return "\x1b[2~"
	case tea.KeyDelete:
		return "\x1b[3~"
	case tea.KeyCtrlA:
		return "\x01"
	case tea.KeyCtrlB:
		return "\x02"
	case tea.KeyCtrlC:
		return "\x03"
	case tea.KeyCtrlD:
		return "\x04"
	case tea.KeyCtrlE:
		return "\x05"
	case tea.KeyCtrlF:
		return "\x06"
	case tea.KeyCtrlG:
		return "\x07"
	case tea.KeyCtrlH:
		return "\x08"
	// tea.KeyCtrlI == tea.KeyTab (both = 9); handled by KeyTab above.
	case tea.KeyCtrlJ:
		return "\x0a"
	case tea.KeyCtrlK:
		return "\x0b"
	case tea.KeyCtrlL:
		return "\x0c"
	// tea.KeyCtrlM == tea.KeyEnter (both = 13); handled by KeyEnter above.
	case tea.KeyCtrlN:
		return "\x0e"
	case tea.KeyCtrlO:
		return "\x0f"
	case tea.KeyCtrlP:
		return "\x10"
	case tea.KeyCtrlQ:
		return "\x11"
	case tea.KeyCtrlR:
		return "\x12"
	case tea.KeyCtrlS:
		return "\x13"
	case tea.KeyCtrlT:
		return "\x14"
	case tea.KeyCtrlU:
		return "\x15"
	case tea.KeyCtrlV:
		return "\x16"
	case tea.KeyCtrlW:
		return "\x17"
	case tea.KeyCtrlX:
		return "\x18"
	case tea.KeyCtrlY:
		return "\x19"
	case tea.KeyCtrlZ:
		return "\x1a"
	case tea.KeyCtrlBackslash:
		return "\x1c"
	case tea.KeyCtrlCloseBracket:
		return "\x1d"
	case tea.KeyCtrlCaret:
		return "\x1e"
	case tea.KeyCtrlUnderscore:
		return "\x1f"
	}
	return ""
}

// ptyOpenedMsg is delivered when a pty.create RPC succeeds.
type ptyOpenedMsg struct {
	sessionID string
	wsID      string
	dataCh    <-chan json.RawMessage
	cancelFn  func()
}

// ptyDataMsg is delivered when a pty.data notification arrives for the active
// session.
type ptyDataMsg struct {
	sessionID string
	data      string
}

// ptyClosedMsg is delivered when the pty.data subscription channel is closed.
type ptyClosedMsg struct {
	sessionID string
}

// ptyErrMsg is delivered when pty.create fails.
type ptyErrMsg struct {
	err error
}

// openPTYCmd fires a pty.create RPC and subscribes to pty.data notifications,
// delivering ptyOpenedMsg on success or ptyErrMsg on failure.
func openPTYCmd(mux *rpc.MuxConn, wsID string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		if mux == nil {
			return ptyErrMsg{err: nil}
		}
		// Subscribe before calling pty.create so we never miss the first bytes.
		dataCh, cancelFn := mux.Subscribe("pty.data")

		var session struct {
			ID string `json:"id"`
		}
		if err := mux.Call("pty.create", map[string]any{
			"workspaceId": wsID,
			// Empty workDir: daemon resolves to the workspace checkout on the host
			// for process/sandbox sessions and to the guest default for VM sessions.
			"workDir": "",
			"cols":    cols,
			"rows":    rows,
		}, &session); err != nil {
			cancelFn()
			return ptyErrMsg{err: err}
		}
		return ptyOpenedMsg{
			sessionID: session.ID,
			wsID:      wsID,
			dataCh:    dataCh,
			cancelFn:  cancelFn,
		}
	}
}

// listenPTYCmd blocks until a pty.data notification arrives for sessionID on
// ch, then returns a ptyDataMsg. Returns ptyClosedMsg if ch is closed.
func listenPTYCmd(ch <-chan json.RawMessage, sessionID string) tea.Cmd {
	return func() tea.Msg {
		for {
			raw, ok := <-ch
			if !ok {
				return ptyClosedMsg{sessionID: sessionID}
			}
			var p struct {
				SessionID string `json:"sessionId"`
				Data      string `json:"data"`
			}
			if err := json.Unmarshal(raw, &p); err != nil || p.SessionID != sessionID {
				continue
			}
			return ptyDataMsg{data: p.Data, sessionID: p.SessionID}
		}
	}
}
