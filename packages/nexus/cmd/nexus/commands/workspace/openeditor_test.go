package workspace

import (
	"testing"
)

func TestGuestSSHHostIsLoopback(t *testing.T) {
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
			if got := guestSSHHostIsLoopback(tc.guestIP); got != tc.want {
				t.Fatalf("guestSSHHostIsLoopback(%q) = %v; want %v", tc.guestIP, got, tc.want)
			}
		})
	}
}

func TestResolveOpenEditorSSH_localDaemonLoopbackGuestIsDirect(t *testing.T) {
	t.Setenv("NEXUS_E2E_DAEMON_WEBSOCKET", "ws://127.0.0.1:7777/")
	t.Setenv("NEXUS_DAEMON_SSH_HOST", "must-not-use-for-direct")
	t.Setenv("NEXUS_DAEMON_SSH_PORT", "")

	direct, proxyJump, jumpPort, _, err := resolveOpenEditorSSH("127.0.0.1:2222")
	if err != nil {
		t.Fatal(err)
	}
	if !direct || proxyJump != "" || jumpPort != 0 {
		t.Fatalf("got direct=%v proxyJump=%q jumpPort=%d; want direct=true and no proxy", direct, proxyJump, jumpPort)
	}
}

func TestResolveOpenEditorSSH_remoteDaemonLoopbackGuestUsesProxy(t *testing.T) {
	t.Setenv("NEXUS_E2E_DAEMON_WEBSOCKET", "ws://10.0.0.99:7777/")
	t.Setenv("NEXUS_DAEMON_SSH_HOST", "jump@engine")
	t.Setenv("NEXUS_DAEMON_SSH_PORT", "")

	direct, proxyJump, _, _, err := resolveOpenEditorSSH("127.0.0.1:2222")
	if err != nil {
		t.Fatal(err)
	}
	if direct || proxyJump != "jump@engine" {
		t.Fatalf("got direct=%v proxyJump=%q; want direct=false proxyJump=jump@engine", direct, proxyJump)
	}
}
