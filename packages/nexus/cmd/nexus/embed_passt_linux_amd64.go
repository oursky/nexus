//go:build linux && amd64

package main

import _ "embed"

// embeddedPasst is the passt user-mode networking daemon, embedded at build time
// so the nexus release binary is fully self-contained. Extracted to
// ~/.local/bin/passt at daemon bootstrap.
//
//go:embed passt-embed
var embeddedPasst []byte
