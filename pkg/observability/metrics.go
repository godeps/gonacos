package observability

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Registry is a thread-safe collection of metrics. The zero value is ready
// to use.
type Registry struct {
	mu       sync.RWMutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
	histos   map[string]*Histogram
}

func NewRegistry() *Registry {
	return &Registry{
		counters: map[string]*Counter{},
		gauges:   map[string]*Gauge{},
		histos:   map[string]*Histogram{},
	}
}

// Counter is a monotonically increasing value.
type Counter struct {
	name   string
	labels map[string]string
	value  atomic.Int64
}

// Gauge is a value that can go up or down.
type Gauge struct {
	name   string
	labels map[string]string
	value  atomic.Int64
}

// Histogram tracks a stream of observations bucketed by upper bound. The
// buckets are fixed at construction time.
type Histogram struct {
	name    string
	labels  map[string]string
	buckets []float64
	counts  []atomic.Int64
	sum     atomic.Int64
	count   atomic.Int64
}

// Counter returns or creates a counter with the given labels. Labels are
// sorted and joined into the key so the same label set always maps to the
// same counter.
func (r *Registry) Counter(name string, labels map[string]string) *Counter {
	key := metricKey(name, labels)
	r.mu.RLock()
	c, ok := r.counters[key]
	r.mu.RUnlock()
	if ok {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[key]; ok {
		return c
	}
	c = &Counter{name: name, labels: copyLabels(labels)}
	r.counters[key] = c
	return c
}

func (c *Counter) Inc()         { c.value.Add(1) }
func (c *Counter) Add(v int64)  { c.value.Add(v) }
func (c *Counter) Value() int64 { return c.value.Load() }

// Gauge returns or creates a gauge with the given labels.
func (r *Registry) Gauge(name string, labels map[string]string) *Gauge {
	key := metricKey(name, labels)
	r.mu.RLock()
	g, ok := r.gauges[key]
	r.mu.RUnlock()
	if ok {
		return g
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[key]; ok {
		return g
	}
	g = &Gauge{name: name, labels: copyLabels(labels)}
	r.gauges[key] = g
	return g
}

func (g *Gauge) Set(v int64)  { g.value.Store(v) }
func (g *Gauge) Add(v int64)  { g.value.Add(v) }
func (g *Gauge) Value() int64 { return g.value.Load() }

// Histogram returns or creates a histogram with the given bucket upper
// bounds. Bucket values are in milliseconds for the standard latency
// convention; callers are responsible for unit consistency.
func (r *Registry) Histogram(name string, labels map[string]string, buckets []float64) *Histogram {
	key := metricKey(name, labels)
	r.mu.RLock()
	h, ok := r.histos[key]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histos[key]; ok {
		return h
	}
	sorted := append([]float64(nil), buckets...)
	sort.Float64s(sorted)
	h = &Histogram{
		name:    name,
		labels:  copyLabels(labels),
		buckets: sorted,
		counts:  make([]atomic.Int64, len(sorted)),
	}
	r.histos[key] = h
	return h
}

// Observe records a value in the histogram. Sum is tracked in integer
// milliseconds; callers should convert before calling.
func (h *Histogram) Observe(ms int64) {
	h.sum.Add(ms)
	h.count.Add(1)
	v := float64(ms)
	for i, ub := range h.buckets {
		if v <= ub {
			h.counts[i].Add(1)
		}
	}
}

// WritePrometheus writes the registry in Prometheus text exposition format.
// Output is sorted by metric name then label set for deterministic diffs.
func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.RLock()
	counters := make([]*Counter, 0, len(r.counters))
	for _, c := range r.counters {
		counters = append(counters, c)
	}
	gauges := make([]*Gauge, 0, len(r.gauges))
	for _, g := range r.gauges {
		gauges = append(gauges, g)
	}
	histos := make([]*Histogram, 0, len(r.histos))
	for _, h := range r.histos {
		histos = append(histos, h)
	}
	r.mu.RUnlock()

	sort.Slice(counters, func(i, j int) bool { return counters[i].name < counters[j].name })
	sort.Slice(gauges, func(i, j int) bool { return gauges[i].name < gauges[j].name })
	sort.Slice(histos, func(i, j int) bool { return histos[i].name < histos[j].name })

	for _, c := range counters {
		writeMetric(w, "# TYPE %s counter\n", c.name)
		writeMetric(w, "%s%s %d\n", c.name, formatLabels(c.labels), c.Value())
	}
	for _, g := range gauges {
		writeMetric(w, "# TYPE %s gauge\n", g.name)
		writeMetric(w, "%s%s %d\n", g.name, formatLabels(g.labels), g.Value())
	}
	for _, h := range histos {
		writeMetric(w, "# TYPE %s histogram\n", h.name)
		cumulative := int64(0)
		for i, ub := range h.buckets {
			cumulative = h.counts[i].Load()
			writeMetric(w, "%s_bucket%s{%s} %d\n", h.name, formatLabels(h.labels), fmt.Sprintf("le=\"%g\"", ub), cumulative)
		}
		writeMetric(w, "%s_bucket%s{le=\"+Inf\"} %d\n", h.name, formatLabels(h.labels), h.count.Load())
		writeMetric(w, "%s_sum%s %d\n", h.name, formatLabels(h.labels), h.sum.Load())
		writeMetric(w, "%s_count%s %d\n", h.name, formatLabels(h.labels), h.count.Load())
	}
}

// writeMetric writes a Prometheus-formatted line to the response writer. The
// error is ignored because the metrics endpoint is best-effort — a broken
// connection should not crash the server.
func writeMetric(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// RegisterProcessMetrics seeds Go runtime and process metrics. The returned
// function should be called to refresh gauges that are not updated on the
// hot path (goroutines, GC count, heap size).
func (r *Registry) RegisterProcessMetrics() func() {
	goroutines := r.Gauge("process_goroutines", nil)
	heapAlloc := r.Gauge("process_heap_alloc_bytes", nil)
	gcCount := r.Gauge("process_gc_count", nil)
	startTime := r.Gauge("process_start_time_seconds", nil)
	startTime.Set(time.Now().Unix())

	refresh := func() {
		goroutines.Set(int64(runtime.NumGoroutine()))
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		heapAlloc.Set(int64(mem.HeapAlloc))
		gcCount.Set(int64(mem.NumGC))
	}
	refresh()
	return refresh
}

// RegisterBuildInfo publishes a constant-1 gauge carrying the binary's
// version, commit, and build date as labels. Standard Prometheus convention:
//
//	gonacos_build_info{version="1.0.0",commit="abc123",build_date="2026-07-05T..."} 1
//
// Operators query `gonacos_build_info` to discover which version is deployed
// across a fleet, and alert on version drift (different versions on different
// nodes) or missing deployments (a version that should be rolled out but
// isn't). The value is always 1 — the information lives in the labels.
//
// Empty strings are accepted so a development build (defaults) still exposes
// the metric; operators see build_date="unknown" and know ldflags were not
// injected.
func (r *Registry) RegisterBuildInfo(version, commit, buildDate string) {
	g := r.Gauge("gonacos_build_info", map[string]string{
		"version":    version,
		"commit":     commit,
		"build_date": buildDate,
	})
	g.Set(1)
}

func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('|')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteString("=\"")
		b.WriteString(labels[k])
		b.WriteString("\"")
	}
	b.WriteByte('}')
	return b.String()
}

func copyLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
