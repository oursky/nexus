package start

import (
	"fmt"
	"strings"
)

// DriverCategory is the user-facing runtime category.
type DriverCategory string

const (
	CategoryVM      DriverCategory = "vm"
	CategoryProcess DriverCategory = "process"
)

// DriverInfo holds the parsed driver category and implementation.
type DriverInfo struct {
	Category       DriverCategory
	Implementation string
}

// ParseDriver normalizes a user-provided driver string into a category and
// concrete implementation. Accepts both category names ("vm", "process") and
// legacy implementation names ("libkrun", "sandbox") for backward compatibility.
func ParseDriver(raw string) (DriverInfo, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "vm", "libkrun":
		return DriverInfo{Category: CategoryVM, Implementation: "libkrun"}, nil
	case "process", "sandbox":
		return DriverInfo{Category: CategoryProcess, Implementation: "sandbox"}, nil
	case "":
		return DriverInfo{}, nil
	default:
		return DriverInfo{}, fmt.Errorf("unsupported driver %q: expected vm, process, libkrun, or sandbox", raw)
	}
}

// IsVM reports whether the driver category is a VM runtime.
func (d DriverInfo) IsVM() bool {
	return d.Category == CategoryVM
}
