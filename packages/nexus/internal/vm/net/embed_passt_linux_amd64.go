//go:build linux && amd64

package net

import _ "embed"

//go:embed assets/passt-linux-amd64
var passtAssetData []byte
