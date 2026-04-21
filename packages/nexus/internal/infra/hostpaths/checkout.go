// Package hostpaths holds filesystem locations shared by the daemon (workspace + PTY).
package hostpaths

import (
	"os"
	"path/filepath"
)

// DaemonCheckoutRoot returns a legacy per-workspace directory (best-effort cleanup on workspace delete).
func DaemonCheckoutRoot() string {
	if e := os.Getenv("NEXUS_DAEMON_CHECKOUT_ROOT"); e != "" {
		return e
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(os.TempDir(), "nexus-daemon-checkouts")
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "nexus", "daemon-checkouts")
}

// CheckoutPathForWorkspace returns the per-workspace checkout directory under [DaemonCheckoutRoot].
func CheckoutPathForWorkspace(workspaceID string) string {
	return filepath.Join(DaemonCheckoutRoot(), workspaceID)
}

// RemoveCheckout deletes a daemon-side checkout tree (best-effort).
func RemoveCheckout(workspaceID string) error {
	p := CheckoutPathForWorkspace(workspaceID)
	if p == "" || p == "/" {
		return nil
	}
	return os.RemoveAll(p)
}
