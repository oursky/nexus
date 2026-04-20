package spotlight

import (
	"testing"
	"time"
)

func TestForwardStateConstants(t *testing.T) {
	if ForwardStateActive == ForwardStateInactive {
		t.Error("ForwardStateActive and ForwardStateInactive must be distinct")
	}
}

func TestForwardSourceConstants(t *testing.T) {
	if ForwardSourceUser == ForwardSourceAuto {
		t.Error("ForwardSourceUser and ForwardSourceAuto must be distinct")
	}
}

func TestExposeSpecFields(t *testing.T) {
	cases := []struct {
		name string
		spec ExposeSpec
	}{
		{
			"user source",
			ExposeSpec{LocalPort: 8080, RemotePort: 8080, Protocol: "tcp", Source: ForwardSourceUser, WorkspaceID: "ws-1"},
		},
		{
			"auto source",
			ExposeSpec{LocalPort: 3000, RemotePort: 3000, Source: ForwardSourceAuto, WorkspaceID: "ws-2"},
		},
		{
			"empty protocol",
			ExposeSpec{LocalPort: 5000, RemotePort: 5000, WorkspaceID: "ws-3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.spec.WorkspaceID == "" {
				t.Error("WorkspaceID should not be empty")
			}
			if tc.spec.LocalPort <= 0 || tc.spec.RemotePort <= 0 {
				t.Error("ports should be positive")
			}
		})
	}
}

func TestForwardFields(t *testing.T) {
	now := time.Now()
	f := Forward{
		ID:          "fwd-1",
		WorkspaceID: "ws-1",
		LocalPort:   8080,
		RemotePort:  8080,
		Protocol:    "tcp",
		State:       ForwardStateActive,
		CreatedAt:   now,
	}
	if f.State != ForwardStateActive {
		t.Errorf("unexpected state: %q", f.State)
	}
	if f.ID == "" {
		t.Error("ID should not be empty")
	}
}

func TestForwardInactiveState(t *testing.T) {
	f := Forward{State: ForwardStateInactive}
	if f.State != ForwardStateInactive {
		t.Errorf("unexpected state: %q", f.State)
	}
}

func TestErrorSentinelsDistinct(t *testing.T) {
	if ErrNotFound == ErrAlreadyExists {
		t.Error("ErrNotFound and ErrAlreadyExists must be distinct")
	}
}
