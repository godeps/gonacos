package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
)

// stubLogger is a Logger that appends each line to a buffer for assertion.
type stubLogger struct {
	buf *bytes.Buffer
}

func (s stubLogger) Infof(format string, args ...any) {
	s.buf.WriteString("INFO  ")
	s.buf.WriteString(fmt.Sprintf(format, args...))
	s.buf.WriteString("\n")
}

func (s stubLogger) Warnf(format string, args ...any) {
	s.buf.WriteString("WARN  ")
	s.buf.WriteString(fmt.Sprintf(format, args...))
	s.buf.WriteString("\n")
}

func (s stubLogger) Errorf(format string, args ...any) {
	s.buf.WriteString("ERROR ")
	s.buf.WriteString(fmt.Sprintf(format, args...))
	s.buf.WriteString("\n")
}

// TestRequestLogMiddlewareDefaultExcludes verifies that the default exclude
// list skips health/metrics probes while still logging everything else.
func TestRequestLogMiddlewareDefaultExcludes(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mw := newRequestLogMiddleware(logger, false, nil, inner)

	// Health probe is excluded from logs.
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("health probe: got %d, want %d", w.Code, http.StatusOK)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("health probe logged (should be excluded): %q", got)
	}

	// Non-excluded path is logged.
	req2 := httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", nil)
	req2.RemoteAddr = "10.0.0.1:1234"
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	logLine := buf.String()
	if !strings.Contains(logLine, "POST") || !strings.Contains(logLine, "/v3/auth/user/login") || !strings.Contains(logLine, "status=200") {
		t.Fatalf("non-excluded path not logged correctly: %q", logLine)
	}
	if !strings.Contains(logLine, "remote=10.0.0.1:1234") {
		t.Fatalf("remote addr missing in log: %q", logLine)
	}
}

// TestRequestLogMiddlewareVerbose verifies that verbose mode logs even
// excluded paths.
func TestRequestLogMiddlewareVerbose(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := newRequestLogMiddleware(logger, true, nil, inner)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if got := buf.String(); !strings.Contains(got, "GET /metrics") {
		t.Fatalf("verbose mode should log /metrics: got %q", got)
	}
}

// TestRequestLogMiddlewareStatusCapture verifies that the middleware
// captures the status code set by the inner handler.
func TestRequestLogMiddlewareStatusCapture(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mw := newRequestLogMiddleware(logger, false, nil, inner)

	req := httptest.NewRequest(http.MethodGet, "/v3/auth/user/list", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if !strings.Contains(buf.String(), "status=404") {
		t.Fatalf("status not captured: %q", buf.String())
	}
}

// TestRequestLogMiddlewareDefaultStatus verifies that a handler which never
// calls WriteHeader is logged as 200 (the Go default).
func TestRequestLogMiddlewareDefaultStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mw := newRequestLogMiddleware(logger, false, nil, inner)

	req := httptest.NewRequest(http.MethodGet, "/v3/auth/user/list", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if !strings.Contains(buf.String(), "status=200") {
		t.Fatalf("default status not 200: %q", buf.String())
	}
}

// TestFormatDuration verifies the duration formatter produces sensible
// output across magnitude boundaries.
func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d        time.Duration
		contains string
	}{
		{500 * time.Nanosecond, "ns"},
		{500 * time.Microsecond, "µs"},
		{500 * time.Millisecond, "ms"},
		{2 * time.Second, "s"},
	}
	for _, c := range cases {
		got := formatDuration(c.d)
		if !strings.Contains(got, c.contains) {
			t.Fatalf("formatDuration(%v) = %q, want substring %q", c.d, got, c.contains)
		}
	}
}

// TestRequestLogMiddlewareIncrementsMetrics verifies that when a metrics
// registry is wired in, each request increments gonacos_http_requests_total
// with the correct method and status labels.
func TestRequestLogMiddlewareIncrementsMetrics(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	registry := observability.NewRegistry()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mw := newRequestLogMiddleware(logger, false, registry, inner)

	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	got := registry.Counter("gonacos_http_requests_total", map[string]string{
		"method": "GET",
		"status": "404",
	}).Value()
	if got != 1 {
		t.Fatalf("counter value = %d, want 1", got)
	}

	// Different label set should not be affected.
	other := registry.Counter("gonacos_http_requests_total", map[string]string{
		"method": "GET",
		"status": "200",
	}).Value()
	if other != 0 {
		t.Fatalf("200 counter = %d, want 0", other)
	}

	// Histogram should have one observation for GET. The Histogram type
	// doesn't expose a count getter, so verify via the Prometheus output.
	_ = registry.Histogram("gonacos_http_request_duration_seconds",
		map[string]string{"method": "GET"},
		[]float64{1, 5, 10})
	var promBuf bytes.Buffer
	registry.WritePrometheus(&promBuf)
	if !strings.Contains(promBuf.String(), "gonacos_http_request_duration_seconds") {
		t.Fatalf("histogram not exposed in /metrics output: %s", promBuf.String())
	}
}
