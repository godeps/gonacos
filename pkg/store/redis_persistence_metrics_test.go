package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
)

// TestRedisPersistenceMetricsSaveSuccess verifies that a successful Save
// increments gonacos_snapshot_saves_total{result="success"}, observes the
// duration histogram, and sets the last-save timestamp gauge to ~now.
//
// These metrics are the data-loss detection signal for operators:
//   - alert on result="failure" rate > 0
//   - alert on time() - last_save_timestamp > 2*interval (stuck loop)
//
// Without these metrics, snapshot failures are silent (the periodic ticker
// previously swallowed errors with _ = p.Save()).
func TestRedisPersistenceMetricsSaveSuccess(t *testing.T) {
	t.Parallel()
	p, _, _, cleanup := newTestPersistence(t, "")
	defer cleanup()

	registry := observability.NewRegistry()
	p.SetMetricsRegistry(registry)

	before := time.Now().Unix()
	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("save: %v", err)
	}
	after := time.Now().Unix()

	successCount := registry.Counter("gonacos_snapshot_saves_total",
		map[string]string{"result": "success"}).Value()
	failureCount := registry.Counter("gonacos_snapshot_saves_total",
		map[string]string{"result": "failure"}).Value()
	if successCount != 1 {
		t.Errorf("saves_total{result=success} = %d, want 1", successCount)
	}
	if failureCount != 0 {
		t.Errorf("saves_total{result=failure} = %d, want 0", failureCount)
	}

	// Histogram count are not exposed via the public API — read them from
	// the Prometheus text exposition.
	out := readSnapshotMetrics(t, registry)
	if !strings.Contains(out, `gonacos_snapshot_save_duration_seconds_count 1`) {
		t.Errorf("expected histogram count=1, got:\n%s", out)
	}

	lastSave := registry.Gauge("gonacos_last_snapshot_save_timestamp_seconds", nil).Value()
	if lastSave < before || lastSave > after+1 {
		t.Errorf("last_save_timestamp = %d, want in [%d, %d]", lastSave, before, after+1)
	}
}

// TestRedisPersistenceMetricsSaveFailure verifies that a Save that errors
// (here, because the redis client is closed mid-call) increments the
// result="failure" counter and does NOT touch the last-save timestamp.
// Preserving the old timestamp on failure is the alerting contract: the
// gauge reflects "when did we last successfully save", not "when did we
// last attempt to save" — a stuck loop attempting failures every interval
// must not refresh the gauge.
func TestRedisPersistenceMetricsSaveFailure(t *testing.T) {
	t.Parallel()
	p, _, c, cleanup := newTestPersistence(t, "")
	defer cleanup()

	registry := observability.NewRegistry()
	p.SetMetricsRegistry(registry)

	// Do one successful save first so the timestamp gauge has a value.
	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	firstSaveTS := registry.Gauge("gonacos_last_snapshot_save_timestamp_seconds", nil).Value()

	// Close the redis client so the next Save fails on Set.
	c.Close()

	if err := p.Save(context.Background()); err == nil {
		t.Fatal("save after client close: expected error, got nil")
	}

	successCount := registry.Counter("gonacos_snapshot_saves_total",
		map[string]string{"result": "success"}).Value()
	failureCount := registry.Counter("gonacos_snapshot_saves_total",
		map[string]string{"result": "failure"}).Value()
	if successCount != 1 {
		t.Errorf("saves_total{result=success} = %d, want 1 (initial save)", successCount)
	}
	if failureCount != 1 {
		t.Errorf("saves_total{result=failure} = %d, want 1 (post-close save)", failureCount)
	}

	// Timestamp gauge must NOT have been refreshed on failure.
	currentTS := registry.Gauge("gonacos_last_snapshot_save_timestamp_seconds", nil).Value()
	if currentTS != firstSaveTS {
		t.Errorf("last_save_timestamp changed on failure: was %d, now %d — gauge must only refresh on success", firstSaveTS, currentTS)
	}
}

// TestRedisPersistenceMetricsLoadSuccess verifies a successful Load
// increments gonacos_snapshot_loads_total{result="success"} exactly once.
// Load is called once at startup, so duration is not tracked (a histogram
// with one observation is not useful).
func TestRedisPersistenceMetricsLoadSuccess(t *testing.T) {
	t.Parallel()
	p, _, _, cleanup := newTestPersistence(t, "")
	defer cleanup()

	registry := observability.NewRegistry()
	p.SetMetricsRegistry(registry)

	// Save first so Load has something to restore.
	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Wipe metrics so we only measure the Load call. We can't reset the
	// existing registry, so use a fresh one.
	freshRegistry := observability.NewRegistry()
	p.SetMetricsRegistry(freshRegistry)

	if err := p.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}

	successCount := freshRegistry.Counter("gonacos_snapshot_loads_total",
		map[string]string{"result": "success"}).Value()
	failureCount := freshRegistry.Counter("gonacos_snapshot_loads_total",
		map[string]string{"result": "failure"}).Value()
	if successCount != 1 {
		t.Errorf("loads_total{result=success} = %d, want 1", successCount)
	}
	if failureCount != 0 {
		t.Errorf("loads_total{result=failure} = %d, want 0", failureCount)
	}
}

// TestRedisPersistenceMetricsFreshStartCountsAsSuccess verifies that a
// fresh start (no snapshot in Redis, no dump file) records as
// result="success" rather than skipping metrics. A fresh start is a normal
// condition, not a failure — but operators still want to see "did Load
// run" in the metrics, so a fresh start counts as success.
func TestRedisPersistenceMetricsFreshStartCountsAsSuccess(t *testing.T) {
	t.Parallel()
	p, _, _, cleanup := newTestPersistence(t, "")
	defer cleanup()

	registry := observability.NewRegistry()
	p.SetMetricsRegistry(registry)

	if err := p.Load(context.Background()); err != nil {
		t.Fatalf("load on fresh start: %v", err)
	}

	successCount := registry.Counter("gonacos_snapshot_loads_total",
		map[string]string{"result": "success"}).Value()
	if successCount != 1 {
		t.Errorf("fresh start loads_total{result=success} = %d, want 1", successCount)
	}
}

// TestRedisPersistenceMetricsNilRegistryNoOp verifies the hook degrades to
// a no-op when no registry is wired. This is the backward-compatibility
// contract: embedders that don't call SetMetricsRegistry still get correct
// Save/Load behavior, just without metrics.
func TestRedisPersistenceMetricsNilRegistryNoOp(t *testing.T) {
	t.Parallel()
	p, _, _, cleanup := newTestPersistence(t, "")
	defer cleanup()
	// Intentionally do NOT call SetMetricsRegistry.

	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("save without registry: %v", err)
	}
	// No panic, no error — the metrics path is silently skipped.
}

// TestRedisPersistenceMetricsPeriodicFailureRecorded verifies that a
// failure during the periodic loop (closed client) is recorded under
// result="failure" — operators see the failure rate climb even though
// Save returns an error that the periodic loop only logs.
func TestRedisPersistenceMetricsPeriodicFailureRecorded(t *testing.T) {
	t.Parallel()
	p, _, c, cleanup := newTestPersistence(t, "")
	defer cleanup()

	registry := observability.NewRegistry()
	p.SetMetricsRegistry(registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := p.StartPeriodic(ctx, 10*time.Millisecond)
	defer stop()

	// Let one successful save land.
	time.Sleep(40 * time.Millisecond)
	c.Close()
	// Let at least one failed save land.
	time.Sleep(40 * time.Millisecond)
	stop()

	failureCount := registry.Counter("gonacos_snapshot_saves_total",
		map[string]string{"result": "failure"}).Value()
	if failureCount == 0 {
		t.Fatal("expected at least one failure from periodic loop after client close, got 0")
	}
}

// readSnapshotMetrics serializes the registry to Prometheus text format so
// tests can assert on histogram count/sum fields (which aren't exposed via
// the public API).
func readSnapshotMetrics(t *testing.T, r *observability.Registry) string {
	t.Helper()
	var buf strings.Builder
	r.WritePrometheus(&buf)
	return buf.String()
}
