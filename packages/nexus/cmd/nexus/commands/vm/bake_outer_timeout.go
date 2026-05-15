//go:build linux || darwin

package vm

import (
	"strings"
	"time"
)

// parseBakeOuterTimeout bounds the enclosing context for bake (must exceed VM-side tool install timeouts).
func parseBakeOuterTimeout(timeoutStr string) (time.Duration, error) {
	if strings.TrimSpace(timeoutStr) == "" {
		// Matches long-running libkrun/macvm bake defaults (often 55m+ inside the VM).
		return 2 * time.Hour, nil
	}
	return time.ParseDuration(timeoutStr)
}
