//go:build !linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var setupVerifyFn = func() error { return fmt.Errorf("not available on non-linux") }
var setupKVMGroupReexecFn = func(commandPath string) error { return fmt.Errorf("not available on non-linux") }
var errKVMGroupRefreshNeeded = errors.New("kvm group refresh not applicable on non-linux")

const setupKVMGroupReexecEnv = "NEXUS_SETUP_KVM_GROUP_REEXEC_NONLINUX"

func setupCommandPath() string {
	if exe, err := os.Executable(); err == nil {
		exe = strings.TrimSpace(exe)
		if exe != "" {
			return exe
		}
	}
	if len(os.Args) > 0 {
		arg0 := strings.TrimSpace(os.Args[0])
		if arg0 != "" {
			if filepath.IsAbs(arg0) {
				return arg0
			}
			if lp, err := exec.LookPath(arg0); err == nil {
				return lp
			}
			return arg0
		}
	}
	return "nexus"
}
