package runtime

import "fmt"

// ErrNotSupportedOnPlatform is returned by Driver methods that are not
// available on the current platform. The UI/CLI should check for this
// error to show a clear "not available on this platform" message rather
// than a generic error.
type ErrNotSupportedOnPlatform struct {
	Op       string
	Platform string
}

func (e *ErrNotSupportedOnPlatform) Error() string {
	return fmt.Sprintf("%s is not supported on %s", e.Op, e.Platform)
}

// NotSupportedOnPlatform returns an ErrNotSupportedOnPlatform for the given
// operation on the current runtime platform.
func NotSupportedOnPlatform(op, platform string) error {
	return &ErrNotSupportedOnPlatform{Op: op, Platform: platform}
}

// Sentinel errors used by registry lookups and driver stubs.
var (
	// ErrUnsupported is returned when an operation is not supported by the driver.
	ErrUnsupported = &ErrNotSupportedOnPlatform{Op: "operation", Platform: "this platform"}
	// ErrDriverNotFound is returned when no driver is registered for the requested backend.
	ErrDriverNotFound = errSentinel("driver not found")
	// ErrSnapshotNotFound is returned when the referenced snapshot does not exist.
	ErrSnapshotNotFound = errSentinel("snapshot not found")
)

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
