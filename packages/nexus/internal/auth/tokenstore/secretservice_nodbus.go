//go:build nodbus

package tokenstore

// secretServiceAvailable always returns false when compiled with -tags nodbus.
func secretServiceAvailable() bool { return false }

// NewSecretServiceStore is unavailable in nodbus builds.
func NewSecretServiceStore() (Store, error) {
	return nil, nil // unreachable; probe() guards the call
}
