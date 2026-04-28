//go:build linux

package libkrun

// BakeStampVersion is bumped whenever the set of tools baked into the
// base rootfs changes, forcing a re-bake on next daemon start.
const BakeStampVersion = "v7"
