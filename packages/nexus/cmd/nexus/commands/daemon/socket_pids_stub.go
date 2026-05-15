//go:build !linux

package daemon

func pidsUsingUnixSocket(string) []int { return nil }
