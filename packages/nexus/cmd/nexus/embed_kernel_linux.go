//go:build linux && libkrun

package main

import _ "embed"

// embeddedKernel is the vmlinux ELF kernel image, built from upstream Linux
// source with libkrunfw's microVM config plus Docker networking additions.
// Extracted to ~/.local/share/nexus/vm/vmlinux.bin at daemon bootstrap.
//
//go:embed assets/vmlinux
var embeddedKernel []byte
