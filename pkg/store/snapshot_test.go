package store

import (
	"encoding/json"
	"sync"
	"testing"
)

// fakeSnapshotter is a test Snapshotter with a thread-safe data field so
// concurrent tests (e.g. TestRedisPersistence_ConcurrentSaveWithRotation)
// can mutate data from multiple goroutines without tripping the race
// detector.
type fakeSnapshotter struct {
	mu   sync.RWMutex
	key  string
	data any
}

func (f *fakeSnapshotter) SnapshotKey() string { return f.key }
func (f *fakeSnapshotter) Snapshot() (any, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.data, nil
}
func (f *fakeSnapshotter) Restore(data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = data
	return nil
}

// setData is a test helper to update the snapshotter's data under the
// mutex.
func (f *fakeSnapshotter) setData(data any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = data
}

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

// TestCoordinatorSnapshotHasChecksum verifies that Snapshot populates the
// Checksum field with a non-empty SHA-256 hex digest, so operators can rely
// on it being present for tamper/corruption detection on disk.
func TestCoordinatorSnapshotHasChecksum(t *testing.T) {
	t.Parallel()
	c := NewCoordinator()
	c.Register(&fakeSnapshotter{key: "a", data: map[string]string{"x": "1"}})
	c.Register(&fakeSnapshotter{key: "b", data: []string{"y", "z"}})

	env, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if env.Checksum == "" {
		t.Fatal("checksum empty after snapshot")
	}
	// SHA-256 hex digest is 64 chars. Verify the format so a future change
	// to a different hash algorithm is caught by the test, not by an operator
	// debugging metrics/alerts that assumed 64-char digests.
	if len(env.Checksum) != 64 {
		t.Fatalf("checksum length = %d, want 64 (SHA-256 hex)", len(env.Checksum))
	}
}

// TestCoordinatorSnapshotChecksumIsDeterministic verifies that two
// consecutive snapshots of the same state produce the same checksum,
// regardless of map iteration order. This is the property that makes the
// checksum useful for diffing snapshots and detecting real modifications
// rather than spurious changes from non-deterministic serialization.
func TestCoordinatorSnapshotChecksumIsDeterministic(t *testing.T) {
	t.Parallel()
	c := NewCoordinator()
	c.Register(&fakeSnapshotter{key: "a", data: map[string]string{"x": "1"}})
	c.Register(&fakeSnapshotter{key: "b", data: []string{"y", "z"}})

	env1, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot 1: %v", err)
	}
	env2, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot 2: %v", err)
	}
	if env1.Checksum != env2.Checksum {
		t.Fatalf("checksum not deterministic: %s vs %s", env1.Checksum, env2.Checksum)
	}
}

// TestCoordinatorSnapshotChecksumChangesWithState verifies that modifying
// the underlying state changes the checksum, so a snapshot of modified
// data is distinguishable from the prior snapshot.
func TestCoordinatorSnapshotChecksumChangesWithState(t *testing.T) {
	t.Parallel()
	c := NewCoordinator()
	a := &fakeSnapshotter{key: "a", data: map[string]string{"x": "1"}}
	c.Register(a)

	env1, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot 1: %v", err)
	}
	a.setData(map[string]string{"x": "2"})
	env2, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot 2: %v", err)
	}
	if env1.Checksum == env2.Checksum {
		t.Fatal("checksum did not change after state modification")
	}
}

// TestCoordinatorRestoreRejectsCorruptedServices verifies that a snapshot
// whose Services map was modified after writing (simulating disk
// corruption or tampering) is rejected by Restore, so corrupted state
// does not silently overwrite in-memory data.
func TestCoordinatorRestoreRejectsCorruptedServices(t *testing.T) {
	t.Parallel()
	c := NewCoordinator()
	c.Register(&fakeSnapshotter{key: "a", data: nil})

	env, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// Tamper with the services map after the checksum was computed.
	env.Services["a"] = "tampered"
	if err := c.Restore(env); err == nil {
		t.Fatal("expected error for corrupted services, got nil")
	}
}

// TestCoordinatorRestoreAcceptsLegacyNoChecksum verifies that a snapshot
// without a Checksum field (written by an older gonacos binary before
// checksums were added) still loads. Backward compatibility: existing
// dump files must not break when upgrading.
func TestCoordinatorRestoreAcceptsLegacyNoChecksum(t *testing.T) {
	t.Parallel()
	c := NewCoordinator()
	a := &fakeSnapshotter{key: "a", data: "before"}
	c.Register(a)

	env := &Envelope{
		Version:  EnvelopeVersion,
		Services: map[string]any{"a": "after"},
		// Checksum intentionally empty — simulates a legacy dump.
	}
	if err := c.Restore(env); err != nil {
		t.Fatalf("restore legacy: %v", err)
	}
	if a.data != "after" {
		t.Fatalf("a.data = %v, want after", a.data)
	}
}

// TestComputeChecksumEmptyMap verifies that an empty services map produces
// an empty checksum (not a hash of "{}"), so empty snapshots are treated
// as legacy/unverified rather than gaining a spurious checksum that would
// fail to match any future state.
func TestComputeChecksumEmptyMap(t *testing.T) {
	got, err := computeChecksum(map[string]any{})
	if err != nil {
		t.Fatalf("computeChecksum: %v", err)
	}
	if got != "" {
		t.Fatalf("empty map checksum = %q, want empty", got)
	}
}

// TestComputeChecksumDeterministic verifies that the same logical state
// produces the same digest regardless of map iteration order. This is the
// property that makes the checksum useful — without it, two snapshots of
// the same data could have different checksums and the verifier would
// reject valid loads.
func TestComputeChecksumDeterministic(t *testing.T) {
	services := map[string]any{
		"namespace": map[string]any{"x": "1", "y": "2"},
		"config":    []any{"a", "b"},
	}
	got1, err := computeChecksum(services)
	if err != nil {
		t.Fatalf("compute1: %v", err)
	}
	got2, err := computeChecksum(services)
	if err != nil {
		t.Fatalf("compute2: %v", err)
	}
	if got1 != got2 {
		t.Fatalf("non-deterministic: %s vs %s", got1, got2)
	}
}
