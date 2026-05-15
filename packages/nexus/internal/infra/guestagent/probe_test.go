package guestagent

import (
	"testing"
	"time"
)

func TestProbeDialDuration(t *testing.T) {
	if d := ProbeDialDuration(); d != 8*time.Second {
		t.Fatalf("ProbeDialDuration: got %v want 8s", d)
	}
}

func TestAgentSocketWaitDuration(t *testing.T) {
	if d := AgentSocketWaitDuration(); d != 15*time.Second {
		t.Fatalf("AgentSocketWaitDuration: got %v want 15s", d)
	}
}

func TestReadinessProbeOuterDuration(t *testing.T) {
	if d := ReadinessProbeOuterDuration(); d != 12*time.Second {
		t.Fatalf("ReadinessProbeOuterDuration: got %v want 12s", d)
	}
}
