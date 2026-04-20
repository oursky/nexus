//go:build darwin && arm64

package mutagenbin

import _ "embed"

// embeddedMutagen is the official Mutagen CLI for darwin/arm64.
//
// Refresh with:
//
//	task mutagen:update
//
// from the repo root (writes mutagen-darwin-arm64 next to this file).
//
//go:embed mutagen-darwin-arm64
var embeddedMutagen []byte
