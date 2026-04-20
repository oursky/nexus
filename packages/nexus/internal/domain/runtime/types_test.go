package runtime

import "testing"

func TestCreateRequestFields(t *testing.T) {
	req := CreateRequest{
		WorkspaceID:   "ws-1",
		WorkspaceName: "my-ws",
		ProjectRoot:   "/projects/foo",
		ConfigBundle:  "bundle-abc",
		Options:       map[string]string{"key": "val"},
	}
	if req.WorkspaceID != "ws-1" {
		t.Errorf("unexpected WorkspaceID: %q", req.WorkspaceID)
	}
	if req.Options["key"] != "val" {
		t.Errorf("unexpected Options value")
	}
}

func TestCreateRequestNilOptions(t *testing.T) {
	req := CreateRequest{WorkspaceID: "ws-2"}
	if req.Options != nil {
		t.Error("Options should be nil by default")
	}
}

func TestSnapshotFields(t *testing.T) {
	snap := Snapshot{ID: "snap-1", WorkspaceID: "ws-1", Path: "/snapshots/snap-1"}
	if snap.ID != "snap-1" {
		t.Errorf("unexpected ID: %q", snap.ID)
	}
	if snap.Path != "/snapshots/snap-1" {
		t.Errorf("unexpected Path: %q", snap.Path)
	}
}

func TestErrorSentinelsDistinct(t *testing.T) {
	errs := []error{ErrUnsupported, ErrDriverNotFound, ErrSnapshotNotFound}
	for i := 0; i < len(errs); i++ {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Errorf("errors[%d] and errors[%d] are the same value", i, j)
			}
		}
	}
}
