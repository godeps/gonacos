package observability

import (
	"bytes"
	"strings"
	"testing"
)

func TestCounterIncrement(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	c := r.Counter("http_requests_total", map[string]string{"method": "GET"})
	c.Inc()
	c.Inc()
	c.Add(3)
	if got := c.Value(); got != 5 {
		t.Fatalf("counter = %d, want 5", got)
	}
}

func TestCounterLabelStability(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	a := r.Counter("x", map[string]string{"a": "1", "b": "2"})
	b := r.Counter("x", map[string]string{"b": "2", "a": "1"})
	if a != b {
		t.Fatal("labels in different order should map to same counter")
	}
}

func TestGaugeSetAndAdd(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	g := r.Gauge("queue_depth", nil)
	g.Set(10)
	g.Add(-3)
	if got := g.Value(); got != 7 {
		t.Fatalf("gauge = %d, want 7", got)
	}
}

func TestHistogramBuckets(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	h := r.Histogram("latency_ms", nil, []float64{1, 5, 10, 50})
	for _, v := range []int64{0, 2, 4, 6, 8, 100} {
		h.Observe(v)
	}
	if h.count.Load() != 6 {
		t.Fatalf("count = %d, want 6", h.count.Load())
	}
	if h.sum.Load() != 120 {
		t.Fatalf("sum = %d, want 120", h.sum.Load())
	}
}

func TestPrometheusOutputFormat(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Counter("requests_total", map[string]string{"code": "200"}).Add(5)
	r.Gauge("inflight", nil).Set(3)
	var sb strings.Builder
	r.WritePrometheus(&sb)
	out := sb.String()
	if !strings.Contains(out, "requests_total{code=\"200\"} 5") {
		t.Fatalf("missing counter line: %s", out)
	}
	if !strings.Contains(out, "inflight 3") {
		t.Fatalf("missing gauge line: %s", out)
	}
	if !strings.Contains(out, "# TYPE requests_total counter") {
		t.Fatalf("missing TYPE line: %s", out)
	}
}

func TestProcessMetricsSeeded(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	refresh := r.RegisterProcessMetrics()
	refresh()
	gauges := r.Gauge("process_goroutines", nil)
	if gauges.Value() == 0 {
		t.Fatal("goroutine gauge should be non-zero after refresh")
	}
}

// TestRegisterBuildInfo verifies that RegisterBuildInfo publishes a
// constant-1 gauge carrying version/commit/build_date labels in the
// Prometheus output, so operators can query `gonacos_build_info` to
// discover which version is deployed.
func TestRegisterBuildInfo(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterBuildInfo("1.2.3", "abc1234", "2026-07-05T00:00:00Z")

	var buf bytes.Buffer
	r.WritePrometheus(&buf)
	out := buf.String()

	if !strings.Contains(out, `gonacos_build_info{build_date="2026-07-05T00:00:00Z",commit="abc1234",version="1.2.3"} 1`) {
		t.Fatalf("build_info line missing or malformed: %s", out)
	}
	if !strings.Contains(out, "# TYPE gonacos_build_info gauge") {
		t.Fatalf("missing TYPE line: %s", out)
	}
}

// TestRegisterBuildInfoDefaults verifies that a development build (where
// ldflags were not injected, so version/commit/build_date are defaults)
// still exposes the metric. Operators see "unknown" and know the binary
// was not built with release ldflags — useful for distinguishing a local
// dev build from a release in production metrics.
func TestRegisterBuildInfoDefaults(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterBuildInfo("0.1.0-dev", "unknown", "unknown")

	g := r.Gauge("gonacos_build_info", map[string]string{
		"version":    "0.1.0-dev",
		"commit":     "unknown",
		"build_date": "unknown",
	})
	if got := g.Value(); got != 1 {
		t.Fatalf("build_info value: got %d, want 1", got)
	}
}

// TestRegisterBuildInfoDistinctLabelSets verifies that calling
// RegisterBuildInfo twice with different values produces two distinct
// gauge entries in the Prometheus output. Prometheus convention treats
// each unique label set as its own metric — the registry does not
// "overwrite" the previous one. Callers should register once at startup.
func TestRegisterBuildInfoDistinctLabelSets(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterBuildInfo("1.0.0", "old", "old-date")
	r.RegisterBuildInfo("2.0.0", "new", "new-date")

	var buf bytes.Buffer
	r.WritePrometheus(&buf)
	out := buf.String()

	if !strings.Contains(out, `version="1.0.0"`) {
		t.Fatalf("first label set missing: %s", out)
	}
	if !strings.Contains(out, `version="2.0.0"`) {
		t.Fatalf("second label set missing: %s", out)
	}
}
