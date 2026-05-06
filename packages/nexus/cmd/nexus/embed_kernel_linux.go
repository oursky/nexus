//go:build linux

package main

import _ "embed"

// embeddedKernel is the vmlinux ELF kernel image, built from upstream Linux
// source with libkrunfw's microVM config plus Docker networking additions.
// Extracted to ~/.cache/nexus/kernels/vmlinux-custom on first bundle run.
//
//go:embed assets/vmlinux
var embeddedKernel []byte
