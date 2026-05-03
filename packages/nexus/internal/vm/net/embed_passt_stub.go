//go:build !linux

package net

var passtAssetData []byte

func extractEmbeddedPasstIfNeeded() error {
	return nil
}
