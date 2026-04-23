//go:build linux

package main

import (
	"io"
)

var initRuntimeBootstrapRunner func(projectRoot, runtimeName string) error = runInitRuntimeBootstrapLinux

func runInitRuntimeBootstrapLinux(projectRoot, runtimeName string) error {
	if runtimeName != "firecracker" {
		return nil
	}
	return RunRootlessBootstrap(io.Discard, false)
}
