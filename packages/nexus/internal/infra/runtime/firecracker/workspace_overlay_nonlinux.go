//go:build !linux

package firecracker

import "fmt"

func EnsureBaseImage(repoRoot, basesDir string) (string, error) {
	return "", fmt.Errorf("EnsureBaseImage not supported on this platform")
}

func ReflinkAvailable(_ string) bool { return false }

func FilesystemType(_ string) string { return "unknown" }

func CreateWorkspaceReflink(_, _ string) error {
	return fmt.Errorf("CreateWorkspaceReflink not supported on this platform")
}
