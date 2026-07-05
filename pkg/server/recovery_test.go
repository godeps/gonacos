package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/observability"
)

func TestRecoveryMiddlewareCatchesPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	mw := newRecoveryMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodGet, "/v3/console/state", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusInternalServerError)
	}
	body := w.Body.String()
	if !strings.Contains(body, "internal server error") {
		t.Errorf("body missing message: %s", body)
	}
	logged := buf.String()
	if !strings.Contains(logged, "panic recovered") {
		t.Errorf("log missing 'panic recovered': %s", logged)
	}
	if !strings.Contains(logged, "rid=") {
		t.Errorf("log missing rid: %s", logged)
	}
}

func TestRecoveryMiddlewarePassesThroughWhenNoPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := newRecoveryMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log lines, got: %s", buf.String())
	}
}

// TestRecoveryMiddlewareWithRegistryIncrementsPanicCounter verifies the
// recovery middleware increments gonacos_http_panics_total when a handler
// panics. The metric is the alerting signal for handler crashes — a non-
// zero rate pages on-call (deployed bug or malformed request). Without
// the metric, panics only show in logs, which are easy to miss under high
// request volume.
func TestRecoveryMiddlewareWithRegistryIncrementsPanicCounter(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	registry := observability.NewRegistry()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	mw := newRecoveryMiddlewareWithRegistry(logger, inner, registry)

	req := httptest.NewRequest(http.MethodGet, "/v3/console/state", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusInternalServerError)
	}
	count := registry.Counter("gonacos_http_panics_total", nil).Value()
	if count != 1 {
		t.Errorf("gonacos_http_panics_total = %d, want 1", count)
	}

	// A second panic should increment the same counter (cache lookup is
	// stable across calls).
	mw.ServeHTTP(httptest.NewRecorder(), req)
	count = registry.Counter("gonacos_http_panics_total", nil).Value()
	if count != 2 {
		t.Errorf("gonacos_http_panics_total after 2 panics = %d, want 2", count)
	}
}

// TestRecoveryMiddlewareNoPanicDoesNotIncrementCounter verifies the counter
// stays at zero when no panic occurs — the metric is purely a signal for
// panics, not for requests.
func TestRecoveryMiddlewareNoPanicDoesNotIncrementCounter(t *testing.T) {
	logger := stubLogger{}
	registry := observability.NewRegistry()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := newRecoveryMiddlewareWithRegistry(logger, inner, registry)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	mw.ServeHTTP(httptest.NewRecorder(), req)

	count := registry.Counter("gonacos_http_panics_total", nil).Value()
	if count != 0 {
		t.Errorf("gonacos_http_panics_total after no panic = %d, want 0", count)
	}
}
