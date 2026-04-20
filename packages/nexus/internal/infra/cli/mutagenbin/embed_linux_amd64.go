//go:build linux && amd64

package mutagenbin

import _ "embed"

// embeddedMutagen is the official Mutagen CLI for linux/amd64 (e.g. Linux dev host or
// cross-compile target). Refresh with: task mutagen:update
//
//go:embed mutagen-linux-amd64
var embeddedMutagen []byte
