package model

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
)

type PanelKind int

const (
	PanelNone PanelKind = iota
	PanelConnect
	PanelSpotlight
	PanelSync
)

const (
	PromptNone            = ""
	PromptSpotPort        = "spot_port"
	PromptSidebarSpotPort = "sidebar_spot_port"
	PromptSyncLocal       = "sync_local"
	PromptSyncDir         = "sync_dir"
	PromptForkChild       = "fork_child"
)

type LayoutPane int

const (
	LayoutPaneLeft   LayoutPane = iota
	LayoutPaneCenter LayoutPane = iota
	LayoutPaneRight  LayoutPane = iota
	LayoutPaneNone   LayoutPane = iota
)

type PtyPaneInterface interface {
	Write(data []byte)
	Render(width, height int) string
	Resize(cols, rows int)
	MouseEnabled() bool
	SendInputCmd(mux *rpc.MuxConn, msg tea.KeyMsg) tea.Cmd
	ResizeCmd(mux *rpc.MuxConn) tea.Cmd
}

type Model struct {
	List   list.Model
	Mux    *rpc.MuxConn
	Detail *workspace.Workspace

	PtyPane         PtyPaneInterface
	PtyWsID         string
	PtyFocused      bool
	PtyDataCh       <-chan json.RawMessage
	CancelPTY       func()
	DaemonOK        bool
	Quitting        bool
	StatusLine      string
	ConfirmDelete   bool
	PendingDeleteID string
	Width           int
	Height          int
	ListWidth       int
	ListHeight      int

	ProjectsByID map[string]string

	Panel        PanelKind
	ShowHelp     bool
	SpotForwards []*spotlight.Forward
	SpotSel      int
	SyncRows     []messages.SyncRow
	SyncSel      int

	Prompt      string
	PromptInput textinput.Model

	CreateMode bool
	CreateStep int
	NameTI     textinput.Model
	RepoTI     textinput.Model
	RefTI      textinput.Model

	PendingSyncPath string

	ShowNoProfile     bool
	NoProfileSel      int
	NoProfileBusy     bool
	NoProfileChecking bool
	NoProfileErr      string
	NoProfileSpinIdx  int
	LocalPort         int

	ShowWizard   bool
	WizardStep   int
	WizardHostTI textinput.Model
	WizardPortTI textinput.Model
	WizardKeyTI  textinput.Model
	WizardErr    string
	WizardBusy   bool

	Tabs           []string
	ActiveTabWS    string
	AutoAttach     bool
	AutoAttachDone bool

	SidebarFocused    bool
	SidebarSel        int
	SidebarFwds       []*spotlight.Forward
	SidebarDiscovered []workspace.DiscoveredPort

	SidebarTunnel        *sshtunnel.MultiTunnel
	SidebarLocalProxy    *sshtunnel.LocalProxy
	SidebarTunnelLive    bool
	SidebarTunnelWsID    string
	SidebarTunnelLocal   bool
	SidebarTunnelErr     string
	SidebarConfirmRemove bool

	LeftPaneRight       int
	CenterPaneRight     int
	MouseSupportEnabled bool
}

type Options struct {
	AutoAttach bool
	Port       int
}

func NewCreateInputs() (name, repo, ref textinput.Model) {
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
