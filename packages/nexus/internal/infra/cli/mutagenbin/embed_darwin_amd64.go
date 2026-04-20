//go:build darwin && amd64

package mutagenbin

import _ "embed"

// embeddedMutagen is the official Mutagen CLI for darwin/amd64.
//
// Refresh with:
//
//	task mutagen:update
//
// from the repo root (writes mutagen-darwin-amd64 next to this file).
//
//go:embed mutagen-darwin-amd64
var embeddedMutagen []byte
