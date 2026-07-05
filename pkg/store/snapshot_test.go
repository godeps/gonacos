package store

import (
	"encoding/json"
	"testing"
)

type fakeSnapshotter struct {
	key  string
	data any
}

func (f *fakeSnapshotter) SnapshotKey() string    { return f.key }
func (f *fakeSnapshotter) Snapshot() (any, error) { return f.data, nil }
func (f *fakeSnapshotter) Restore(data any) error { f.data = data; return nil }

func TestCoordinatorSnapshotAndRestore(t *testing.T) {
	t.Parallel()
	c := NewCoordinator()
	a := &fakeSnapshotter{key: "a", data: map[string]string{"x": "1"}}
	b := &fakeSnapshotter{key: "b", data: []string{"y", "z"}}
	c.Register(a)
	c.Register(b)

	env, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if env.Version != EnvelopeVersion {
		t.Fatalf("version = %v", env.Version)
	}
	if len(env.Services) != 2 {
		t.Fatalf("services = %d, want 2", len(env.Services))
	}
	if _, ok := env.Services["a"]; !ok {
		t.Fatal("missing service a")
	}

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Envelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Restore(&decoded); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

func TestCoordinatorRestoreRejectsBadEnvelope(t *testing.T) {
	t.Parallel()
	c := NewCoordinator()
	if err := c.Restore(nil); err == nil {
		t.Fatal("expected error for nil envelope")
	}
	if err := c.Restore(&Envelope{Version: ""}); err == nil {
		t.Fatal("expected error for missing version")
	}
	if err := c.Restore(&Envelope{Version: "v1"}); err == nil {
		t.Fatal("expected error for missing services map")
	}
}

func TestCoordinatorSkipsUnknownRestoreKeys(t *testing.T) {
	t.Parallel()
	c := NewCoordinator()
	a := &fakeSnapshotter{key: "a", data: "before"}
	c.Register(a)
	env := &Envelope{
		Version:  "v1",
		Services: map[string]any{"a": "after", "unknown": "x"},
	}
	if err := c.Restore(env); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if a.data != "after" {
		t.Fatalf("a.data = %v, want after", a.data)
	}
}
