// Package observability provides a minimal, zero-dependency metrics registry
// with Prometheus text-format export. It is intentionally not a full
// replacement for prometheus/client_golang; it covers the counters, gauges,
// and histograms needed by the ops endpoint and the acceptance test gates.
package observability
