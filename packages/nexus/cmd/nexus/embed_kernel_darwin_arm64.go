//go:build darwin && arm64

package main

import _ "embed"

// embeddedKernel is the arm64 raw kernel image used for macOS microVMs.
// Extracted to ~/.cache/nexus/kernels/Image-custom on first bundle run.
//
//go:embed assets/Image
var embeddedKernel []byte
