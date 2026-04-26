//go:build linux

package libkrun

import "testing"

func TestParseNameserversFromResolvConf(t *testing.T) {
	content := `
search example.local
nameserver 127.0.0.53
nameserver 10.10.0.2
nameserver 10.10.0.3
nameserver 10.10.0.2
`
	got := parseNameserversFromResolvConf(content)
	if len(got) != 2 {
		t.Fatalf("expected 2 nameservers, got %d (%v)", len(got), got)
	}
	if got[0] != "10.10.0.2" || got[1] != "10.10.0.3" {
		t.Fatalf("unexpected nameservers: %v", got)
	}
}

func TestParseNameserversFromResolvConfIgnoresInvalid(t *testing.T) {
	content := "nameserver not-an-ip\nnameserver 127.0.0.1\n"
	got := parseNameserversFromResolvConf(content)
	if len(got) != 0 {
		t.Fatalf("expected no usable nameservers, got %v", got)
	}
}
