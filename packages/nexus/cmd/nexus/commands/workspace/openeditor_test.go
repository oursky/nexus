package workspace

import "testing"

func TestGuestSSHIsDirect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		guestIP string
		want    bool
	}{
		{"127.0.0.1:49832", true},
		{"127.0.0.1", true},
		{"localhost:2222", true},
		{"[::1]:22", true},
		{"10.0.2.15", false},
		{"10.0.2.15:22", false},
		{"192.168.1.1:22", false},
	}
	for _, tc := range cases {
		t.Run(tc.guestIP, func(t *testing.T) {
			t.Parallel()
			if got := guestSSHIsDirect(tc.guestIP); got != tc.want {
				t.Fatalf("guestSSHIsDirect(%q) = %v; want %v", tc.guestIP, got, tc.want)
			}
		})
	}
}
