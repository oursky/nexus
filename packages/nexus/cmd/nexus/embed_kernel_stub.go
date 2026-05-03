//go:build !(linux && libkrun) && !darwin

//nolint:unused
package main

// embeddedKernel is empty for non-libkrun builds.
var embeddedKernel []byte
