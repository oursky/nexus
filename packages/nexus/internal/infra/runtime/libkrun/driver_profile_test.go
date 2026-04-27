//go:build linux

package libkrun

import "testing"

func TestNormalizeGuestVMProfile(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "default"},
		{"default", "default"},
		{"minimal", "minimal"},
		{"virtiofs", "default"},
		{"DEV", "default"},
		{"unknown", "default"},
	}
	for _, tc := range cases {
		if got := normalizeGuestVMProfile(tc.in); got != tc.want {
			t.Fatalf("normalizeGuestVMProfile(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
