package messages

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
