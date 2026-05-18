package messages

import (
	"encoding/json"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/sshtunnel"
)

// SyncRow is a single sync session row for the sync list panel.
type SyncRow struct {
	SessionID   string
	WorkspaceID string
	LocalPath   string
	Status      string
	Direction   string
}

// Connection messages.
type ConnReady struct{ Mux *rpc.MuxConn }
type ConnErr struct{ Err error }

// Workspace list messages.
type Workspaces struct {
	Workspaces   []workspace.Workspace
	ProjectsByID map[string]string // project ID → name; may be nil
}
type WorkspacesErr struct{ Err error }

// Refresh ticker.
type RefreshTick struct{}

// Detail messages.
type DetailLoaded struct{ Ws workspace.Workspace }
type DetailErr struct{ Err error }

// Mutation / generic operation done.
type MutationDone struct{ Err error }

// Spotlight panel messages.
type SpotlightsData struct {
	Forwards []*spotlight.Forward
	Err      error
}

// Sync panel messages.
type SyncListData struct {
	Rows []SyncRow
	Err  error
}

// Workspace creation / fork messages.
type CreateDone struct{ Err error }
type ForkDone struct{ Err error }

// Shell / attach messages.
type ShellReturned struct {
	Err  error
	WsID string // ID of the workspace whose shell just exited
}

// Three-pane sidebar spotlight messages.
type SidebarSpot struct {
	WsID     string
	Forwards []*spotlight.Forward
	Err      error
}

// SidebarSpotTick fires every 3s to refresh the spotlight sidebar.
type SidebarSpotTick struct{}

// SidebarDiscovered delivers workspace.discover-ports results for the sidebar.
type SidebarDiscovered struct {
	WsID  string
	Ports []workspace.DiscoveredPort
	Err   error
}

// TunnelStarted is delivered when the background SSH tunnel goroutine
// finishes starting (or fails). Tunnel is nil when there's nothing to tunnel
// (e.g. local daemon, no active forwards, or error).
type TunnelStarted struct {
	WsID       string
	Tunnel     *sshtunnel.MultiTunnel
	LocalProxy *sshtunnel.LocalProxy // non-nil when local daemon needs port remapping (libkrun VMs)
	BoundPorts []int
	Local      bool // true when daemon is local — no SSH tunnel needed
	Err        error
}

// PTY messages.

type PtyOpened struct {
	SessionID string
	WsID      string
	DataCh    <-chan json.RawMessage
	CancelFn  func()
}

type PtyData struct {
	SessionID string
	Data      string
}

type PtyClosed struct{ SessionID string }

type PtyErr struct{ Err error }

// No-profile messages.

type LocalCheck struct{ Alive bool }

type DaemonStartDone struct{ Err error }

type NoProfileSpinTick struct{}

// Connect wizard messages.

type WizardSaved struct{}

type WizardErr struct{ Err error }
