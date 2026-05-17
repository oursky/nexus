package messages

// WorkspaceListReceived is sent when the workspace list is fetched.
type WorkspaceListReceived struct {
    Workspaces []WorkspaceItem
}

// WorkspaceItem represents a workspace for the TUI list.
type WorkspaceItem struct {
    ID       string
    Name     string
    Repo     string
    State    string
    Project  string
}

// FilterValue implements list.Item interface.
func (w WorkspaceItem) FilterValue() string {
    return w.Name + " " + w.Repo + " " + w.State + " " + w.Project
}

// String implements fmt.Stringer for debugging.
func (w WorkspaceItem) String() string {
    return w.Name
}

// Title returns the display title for list rendering.
func (w WorkspaceItem) Title() string {
    return w.Name
}

// Description returns the description for list rendering.
func (w WorkspaceItem) Description() string {
    return w.Repo + " • " + w.State
}

// WorkspaceStateChanged is sent when a workspace state changes.
type WorkspaceStateChanged struct {
    ID    string
    State string
}

// WorkspaceSelected is sent when a workspace is selected (highlighted).
type WorkspaceSelected struct {
    ID string
}

// WorkspaceDetailRequested is sent when user requests workspace detail.
type WorkspaceDetailRequested struct {
    ID string
}

// WorkspaceCreateRequested is sent when user requests workspace creation.
type WorkspaceCreateRequested struct{}

// WorkspaceDeleteRequested is sent when user requests workspace deletion.
type WorkspaceDeleteRequested struct {
    ID string
}

// WorkspaceDeleteConfirmed is sent after deletion confirmation.
type WorkspaceDeleteConfirmed struct {
    ID string
}

// WorkspaceStartRequested is sent when user requests workspace start.
type WorkspaceStartRequested struct {
    ID string
}

// WorkspaceStopRequested is sent when user requests workspace stop.
type WorkspaceStopRequested struct {
    ID string
}

// WorkspaceFilterChanged is sent when the workspace filter text changes.
type WorkspaceFilterChanged struct {
    Text string
}
