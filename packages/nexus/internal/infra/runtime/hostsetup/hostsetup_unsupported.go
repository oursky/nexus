//go:build !linux && !darwin

package hostsetup

import "fmt"

func unsupported() error {
	return fmt.Errorf("hostsetup: unsupported platform; only Linux (amd64) and macOS (arm64) are supported")
}
