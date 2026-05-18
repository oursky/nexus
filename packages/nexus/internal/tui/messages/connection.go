package messages

import "github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"

// DaemonConnected is sent when the daemon connection is established.
type DaemonConnected struct{}

// DaemonDisconnected is sent when the daemon connection is lost.
type DaemonDisconnected struct {
    Error error
}

// DaemonConnecting is sent while connecting to the daemon.
type DaemonConnecting struct{}

// ConnectionStatusMsg is sent to update the connection status line.
type ConnectionStatusMsg struct {
    Status string
}

// ConnReadyMsg is sent when daemon connection is established.
type ConnReadyMsg struct {
    Mux *rpc.MuxConn
}

// ConnFailedMsg is sent when daemon connection fails.
type ConnFailedMsg struct {
    Error error
}

// WizardSubmitMsg is sent when the connect wizard form is submitted.
type WizardSubmitMsg struct {
    Host string
    Port string
    Key  string
}
