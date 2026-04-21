//go:build !((darwin || linux) && (amd64 || arm64))

package mutagenbin

// embeddedMutagen is empty on platforms without a bundled Mutagen; Path uses PATH.
var embeddedMutagen []byte
