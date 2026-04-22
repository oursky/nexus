package spotlight

import "testing"

func TestBackendUsesGuestControlChannel(t *testing.T) {
	tests := []struct {
		backend string
		want    bool
	}{
		{backend: "firecracker", want: true},
		{backend: "krun", want: true},
		{backend: "KRun", want: true},
		{backend: "process", want: false},
		{backend: "", want: false},
	}

	for _, tt := range tests {
		if got := backendUsesGuestControlChannel(tt.backend); got != tt.want {
			t.Fatalf("backend=%q got=%v want=%v", tt.backend, got, tt.want)
		}
	}
}
