package messages

// QuitRequested is sent when user requests quit.
type QuitRequested struct{}

// QuitConfirmed is sent after quit confirmation.
type QuitConfirmed struct{}

// ViewChanged is sent when the active view changes.
type ViewChanged struct {
    View ViewKind
}

type ViewKind int

const (
    ViewDashboard ViewKind = iota
    ViewOnramp
    ViewCreate
    ViewDetail
    ViewSpotlight
    ViewSync
    ViewHelp
    ViewConnect
)

// HelpRequested is sent when user requests help.
type HelpRequested struct{}

// HelpClosed is sent when help overlay is closed.
type HelpClosed struct{}

// ModalRequested is sent to show a modal.
type ModalRequested struct {
    Title   string
    Body    string
    Actions []string
}

// ModalClosed is sent when modal is closed.
type ModalClosed struct{}

// ToastShown is sent to show a toast.
type ToastShown struct {
    Message string
    Kind    ToastKind
}

type ToastKind int

const (
    ToastSuccess ToastKind = iota
    ToastError
    ToastWarning
    ToastInfo
)
