//go:build !linux

package firecracker

import "fmt"

func EnsureBaseImage(repoRoot, basesDir string) (string, error) {
	return "", fmt.Errorf("EnsureBaseImage not supported on this platform")
}

func createWorkspaceOverlay(overlayPath string) error {
	return fmt.Errorf("createWorkspaceOverlay not supported on this platform")
}
