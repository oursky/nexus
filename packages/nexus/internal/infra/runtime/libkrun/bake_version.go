//go:build linux

package libkrun

// BakeStampVersion is bumped whenever the set of tools baked into the
// base rootfs changes, forcing a re-bake on next daemon start.
// v16: upgraded base OS from Ubuntu 24.04 LTS to Ubuntu 26.04 LTS.
// v17: removed nodejs/mise/npx wrappers from base bake — plain sandbox by default.
// v18: pre-pull nginx:alpine and alpine Docker images during bake for CI.
// v19: slimmer bake (doc/locale/cache purge, apt autoremove/suggests off); release zstd rootfs primary.
const BakeStampVersion = "v19"
