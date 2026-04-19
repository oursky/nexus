package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func ErrNotAbsProjectRoot(what, raw string) error {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return fmt.Errorf("%s must be an absolute path (could not resolve %q: %v)", what, raw, err)
	}
	return fmt.Errorf("%s must be an absolute path (got %q); resolved: %q", what, raw, abs)
}

func FormatCommand(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, ShellQuote(command))
	for _, arg := range args {
		parts = append(parts, ShellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n\r\"'`$\\") {
		return strconv.Quote(value)
	}
	return value
}

func SetupCommandPath() string {
	if exe, err := os.Executable(); err == nil {
		exe = strings.TrimSpace(exe)
		if exe != "" {
			return exe
		}
	}
	return "nexus"
}

func NormalizeRuntimeBackend(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "firecracker":
		return "firecracker", true
	case "process":
		return "process", true
	default:
		return "", false
	}
}

func ErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func HostGOOS() string {
	return runtime.GOOS
}
