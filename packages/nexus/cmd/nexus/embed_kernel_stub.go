//go:build !(linux && libkrun)

package main

// embeddedKernel is empty for non-libkrun builds.
var embeddedKernel []byte
