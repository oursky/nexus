package messages

// SpotlightRequested is sent when user opens spotlight panel.
type SpotlightRequested struct{}

// SpotlightClosed is sent when spotlight panel is closed.
type SpotlightClosed struct{}

// SpotlightRefreshRequested is sent when user requests refresh.
type SpotlightRefreshRequested struct{}

// PortForwardAdded is sent when a port forward is added.
type PortForwardAdded struct {
    Port   int
    Label  string
}

// PortForwardRemoved is sent when a port forward is removed.
type PortForwardRemoved struct {
    Port int
}

// PortForwardToggled is sent when a port forward is toggled.
type PortForwardToggled struct {
    Port int
}

// PortDiscovered is sent when a port is discovered.
type PortDiscovered struct {
    Port    int
    Process string
}
