package pty

import "testing"

func TestBackendUsesGuestControlChannel(t *testing.T) {
	tests := []struct {
		backend string
		want    bool
	}{
		{backend: "process", want: false},
		{backend: "", want: true},
		{backend: "libkrun", want: true},
		{backend: "Libkrun", want: true},
	}

	for _, tt := range tests {
		if got := backendUsesGuestControlChannel(tt.backend); got != tt.want {
			t.Fatalf("backend=%q got=%v want=%v", tt.backend, got, tt.want)
		}
	}
}
