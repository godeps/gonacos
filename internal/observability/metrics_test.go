package observability

import (
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
