//go:build !linux

package firecracker

import (
	"errors"
	"fmt"
)

const bridgeName = "nexusbr0"

func checkTapHelper() error {
	return errors.New("tap helper is only available on Linux")
}

func checkBridge() error {
	return errors.New("bridge is only available on Linux")
}

func tapHelperSetupInstructions() string {
	return "not applicable on non-Linux systems"
}

func bridgeSetupInstructions() string {
	return "not applicable on non-Linux systems"
}

func realSetupTAP(tapName, hostIP, subnetCIDR string) (any, error) {
	return nil, fmt.Errorf("TAP devices are only supported on Linux")
}

func realTeardownTAP(tapName, subnetCIDR string) {
}
