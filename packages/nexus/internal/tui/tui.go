package tui

import (
	"encoding/json"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/sshtunnel"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/pty"
)

type panelKind int

const (
	panelNone panelKind = iota
	panelConnect
	panelSpotlight
	panelSync
)

const (
	promptNone            = ""
	promptSpotPort        = "spot_port"
	promptSidebarSpotPort = "sidebar_spot_port"
	promptSyncLocal       = "sync_local"
	promptSyncDir         = "sync_dir"
	promptForkChild       = "fork_child"
)

// layoutPane identifies which pane contains a given screen column (for mouse
// hit-testing).
type layoutPane int

const (
	layoutPaneLeft   layoutPane = iota
	layoutPaneCenter layoutPane = iota
	layoutPaneRight  layoutPane = iota
	layoutPaneNone   layoutPane = iota
)

// Model is the root Bubble Tea model for the Nexus TUI.
type Model struct {
	list   list.Model
	mux    *rpc.MuxConn
	detail *workspace.Workspace

	// Split-pane PTY state (right pane; active when width >= 100).
	ptyPane         *pty.PtyPane
	ptyWsID         string                 // workspace ID of the active PTY (or "")
	ptyFocused      bool                   // true when right pane has keyboard focus
	ptyDataCh       <-chan json.RawMessage // open pty.data subscription
	cancelPTY       func()                 // unsubscribes and closes ptyDataCh
	daemonOK        bool
	quitting        bool
	statusLine      string
	confirmDelete   bool
	pendingDeleteID string
	width           int
	height          int
	listWidth       int
	listHeight      int

	projectsByID map[string]string // project ID → name (refreshed with workspace list)

	panel        panelKind
	showHelp     bool
	spotForwards []*spotlight.Forward
	spotSel      int
	syncRows     []messages.SyncRow
	syncSel      int

	prompt      string
	promptInput textinput.Model

	createMode bool
	createStep int
	nameTI     textinput.Model
	repoTI     textinput.Model
	refTI      textinput.Model

	pendingSyncPath string

	// no-profile view — shown on first run when no profile exists
	showNoProfile     bool
	noProfileSel      int  // 0=start local, 1=connect remote
	noProfileBusy     bool // spinning while starting daemon
	noProfileChecking bool // waiting for local daemon check result
	noProfileErr      string
	noProfileSpinIdx  int
	localPort         int // port to use for local daemon operations

	// connect wizard — shown when no daemon profile is configured
	showWizard   bool
	wizardStep   int // 0=host, 1=port, 2=sshkey
	wizardHostTI textinput.Model
	wizardPortTI textinput.Model
	wizardKeyTI  textinput.Model
	wizardErr    string
	wizardBusy   bool

	// session tabs — workspaces attached at least once this TUI session
	tabs           []string // workspace IDs in order
	activeTabWS    string   // last attached workspace ID
	autoAttach     bool     // --auto-attach flag
	autoAttachDone bool     // prevent multiple auto-attaches

	// Three-pane spotlight sidebar (visible when width >= 110).
	sidebarFocused    bool
	sidebarSel        int
	sidebarFwds       []*spotlight.Forward
	sidebarDiscovered []workspace.DiscoveredPort // workspace.discover-ports result

	// SSH tunnel for spotlight port forwarding (client-side, one per workspace).
	// nil when no tunnel is running. Closed on quit, workspace switch, or when
	// all active forwards are removed.
	sidebarTunnel      *sshtunnel.MultiTunnel
	sidebarLocalProxy  *sshtunnel.LocalProxy // local TCP proxy for libkrun VM workspaces on local daemon
	sidebarTunnelLive  bool                  // true once tunnel/proxy has successfully started
	sidebarTunnelWsID  string                // workspace ID the running tunnel belongs to
	sidebarTunnelLocal bool                  // true when daemon is local (no SSH needed)

	// sidebarTunnelErr holds the raw error string from the last failed tunnel
	// start attempt. Displayed verbatim in the spotlight sidebar.
	sidebarTunnelErr string

	// sidebarConfirmRemove is true while waiting for the user to confirm [y/n]
	// before removing the currently selected spotlight forward via [x].
	sidebarConfirmRemove bool

	// Pane x-boundaries for mouse hit-testing (0-indexed, inner coordinates,
	// i.e. after removing the 2-column outer padding).
	// leftPaneRight  = last inner column of the left pane (= leftW - 1)
	// centerPaneRight = last inner column of the center pane (= leftW + centerW)
	leftPaneRight   int
	centerPaneRight int

	// mouseSupportEnabled is set to true after the first WindowSizeMsg so that
	// mouse coordinates are valid before we start hit-testing.
	mouseSupportEnabled bool
}

// Options configures a TUI run.
type Options struct {
	// AutoAttach re-attaches to the last active workspace on startup
	// (if it is in running state). Requires --auto-attach flag; NOT default.
	AutoAttach bool
	// Port is the local daemon port for the no-profile check/start flow.
	// 0 means use the compiled-in default (7777 prod, 7778 dev).
	Port int
}

// Run starts the interactive TUI until the user quits.
func Run(opts ...Options) error {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	p := tea.NewProgram(NewModel(o), tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := p.Run()
	return err
}

// NewModel constructs an initial model (connection opens during Init).
func NewModel(opts ...Options) Model {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	l := newWorkspaceList(80, 20)
	l.SetFilteringEnabled(true)

	pi := textinput.New()
	pi.Prompt = "> "
	pi.CharLimit = 512

	nt, rt, rf := newCreateInputs()

	// Restore session state from disk.
	ss := loadSessionState()

	lp := o.Port
	if lp == 0 {
		lp = 7777
	}

	return Model{
		list:        l,
		statusLine:  "connecting…",
		promptInput: pi,
		nameTI:      nt,
		repoTI:      rt,
		refTI:       rf,
		tabs:        ss.Tabs,
		activeTabWS: ss.Active,
		autoAttach:  o.AutoAttach,
		localPort:   lp,
	}
}

func newCreateInputs() (name, repo, ref textinput.Model) {
	name = textinput.New()
	name.Placeholder = "name (required)"
	name.CharLimit = 120
	name.Width = 56

	repo = textinput.New()
	repo.Placeholder = "repo path on engine (required)"
	repo.CharLimit = 512
	repo.Width = 56

	ref = textinput.New()
	ref.Placeholder = "ref / branch (optional)"
	ref.CharLimit = 200
	ref.Width = 56
	return name, repo, ref
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return openDaemonConn()
}
