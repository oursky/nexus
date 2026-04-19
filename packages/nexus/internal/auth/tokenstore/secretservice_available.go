//go:build !nodbus

package tokenstore

// secretServiceAvailable reports whether the D-Bus Secret Service is reachable.
func secretServiceAvailable() bool { return IsAvailable() }
