package rpc

import (
	"testing"
)

func TestDaemonEndpointIsLocal_FromE2EWebSocketURL(t *testing.T) {
	cases := []struct {
		wsURL string
		want  bool
	}{
		{"ws://127.0.0.1:7777/", true},
		{"ws://localhost:7799/", true},
		{"ws://[::1]:1234/", true},
		{"ws://10.0.0.5:7777/", false},
		{"ws://engine.example:7777/", false},
		{"not-a-url", false},
	}
	for _, tc := range cases {
		t.Run(tc.wsURL, func(t *testing.T) {
			t.Setenv(envE2EDaemonWebSocket, tc.wsURL)
			if got := DaemonEndpointIsLocal(); got != tc.want {
				t.Fatalf("DaemonEndpointIsLocal() = %v; want %v (url %q)", got, tc.want, tc.wsURL)
			}
		})
	}
}
