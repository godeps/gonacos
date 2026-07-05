package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/observability"
)

// captureAuditLogger is an AuditLogger that records every event for
// assertion. Used by the metrics tests to verify the wrapped logger
// still receives events after the metrics wrapper is applied.
type captureAuditLogger struct {
	events []AuditEvent
}

func (c *captureAuditLogger) Log(e AuditEvent) {
	c.events = append(c.events, e)
}

// TestMetricsAuditLoggerIncrementsCounter verifies that
// WrapWithMetrics returns a logger that increments
// gonacos_audit_events_total{action,result} on every event, with the
// correct labels derived from the event's Action and Result fields.
//
// Without these metrics, an operator can't alert on "audit rate
// spiked" — a burst of login_failed events is a brute-force attempt,
// but it's invisible in /metrics without the counter.
func TestMetricsAuditLoggerIncrementsCounter(t *testing.T) {
	// Reset package state so the test is hermetic.
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	capture := &captureAuditLogger{}
	wrapped := WrapWithMetrics(capture)

	events := []AuditEvent{
		{Action: AuditActionLogin, Result: AuditResultSuccess},
		{Action: AuditActionLoginFailed, Result: AuditResultFailure},
		{Action: AuditActionUserCreate, Result: AuditResultSuccess},
		{Action: AuditActionUserCreate, Result: AuditResultFailure},
		{Action: AuditActionConfigPublish, Result: AuditResultSuccess},
	}
	for _, e := range events {
		wrapped.Log(e)
	}

	// Verify the wrapped logger still received every event.
	if len(capture.events) != len(events) {
		t.Errorf("wrapped logger received %d events, want %d", len(capture.events), len(events))
	}

	// Verify the counter values per label set.
	cases := []struct {
		action string
		result string
		want   int64
	}{
		{string(AuditActionLogin), string(AuditResultSuccess), 1},
		{string(AuditActionLoginFailed), string(AuditResultFailure), 1},
		{string(AuditActionUserCreate), string(AuditResultSuccess), 1},
		{string(AuditActionUserCreate), string(AuditResultFailure), 1},
		{string(AuditActionConfigPublish), string(AuditResultSuccess), 1},
	}
	for _, c := range cases {
		got := registry.Counter("gonacos_audit_events_total", map[string]string{
			"action": c.action,
			"result": c.result,
		}).Value()
		if got != c.want {
			t.Errorf("counter{action=%s, result=%s} = %d, want %d", c.action, c.result, got, c.want)
		}
	}
}

// TestMetricsAuditLoggerMultipleEventsSameLabels verifies that
// repeated events with the same labels accumulate — the counter is
// cumulative, not per-event.
func TestMetricsAuditLoggerMultipleEventsSameLabels(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	capture := &captureAuditLogger{}
	wrapped := WrapWithMetrics(capture)

	for i := 0; i < 5; i++ {
		wrapped.Log(AuditEvent{Action: AuditActionLoginFailed, Result: AuditResultFailure})
	}

	got := registry.Counter("gonacos_audit_events_total", map[string]string{
		"action": string(AuditActionLoginFailed),
		"result": string(AuditResultFailure),
	}).Value()
	if got != 5 {
		t.Errorf("login_failed counter = %d, want 5 (5 brute-force attempts)", got)
	}
}

// TestWrapWithMetricsNoRegistryReturnsOriginal verifies that when no
// metrics registry is configured, WrapWithMetrics returns the original
// logger unchanged — backward compatible with embedders that don't
// wire observability.
func TestWrapWithMetricsNoRegistryReturnsOriginal(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	AuditMetricsRegistry = nil

	capture := &captureAuditLogger{}
	wrapped := WrapWithMetrics(capture)
	if wrapped != capture {
		t.Errorf("WrapWithMetrics with nil registry should return original logger, got %T", wrapped)
	}
}

// TestWrapWithMetricsNilLoggerReturnsNil verifies that
// WrapWithMetrics(nil) returns nil rather than panicking — the audit
// pipeline treats nil as "audit disabled" and the metrics wrapper must
// preserve that contract.
func TestWrapWithMetricsNilLoggerReturnsNil(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	if got := WrapWithMetrics(nil); got != nil {
		t.Errorf("WrapWithMetrics(nil) = %v, want nil", got)
	}
}

// TestMetricsAuditLoggerExposedInPrometheusOutput verifies that the
// counter appears in the Prometheus /metrics output with the correct
// name and labels, so scrapers pick it up.
func TestMetricsAuditLoggerExposedInPrometheusOutput(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	wrapped := WrapWithMetrics(&captureAuditLogger{})
	wrapped.Log(AuditEvent{Action: AuditActionLogin, Result: AuditResultSuccess})

	var buf bytes.Buffer
	registry.WritePrometheus(&buf)
	out := buf.String()
	if !strings.Contains(out, "gonacos_audit_events_total") {
		t.Errorf("metric missing from /metrics output: %s", out)
	}
	if !strings.Contains(out, "action=\"login\"") {
		t.Errorf("action label missing: %s", out)
	}
	if !strings.Contains(out, "result=\"success\"") {
		t.Errorf("result label missing: %s", out)
	}
}
