package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReadinessHandlerNilChecker verifies that a nil checker (the legacy
// default) returns 200/ok.
func TestReadinessHandlerNilChecker(t *testing.T) {
	h := readinessHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/readiness", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("nil checker: got %d, want %d", w.Code, http.StatusOK)
	}
}

// TestReadinessHandlerReady verifies that a checker returning nil produces
// 200/ok.
func TestReadinessHandlerReady(t *testing.T) {
	checker := ReadinessCheckerFunc(func(ctx context.Context) error { return nil })
	h := readinessHandler(checker)
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/readiness", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ready checker: got %d, want %d", w.Code, http.StatusOK)
	}
}

// TestReadinessHandlerNotReady verifies that a checker returning an error
// produces 503 with the error message in the body.
func TestReadinessHandlerNotReady(t *testing.T) {
	checker := ReadinessCheckerFunc(func(ctx context.Context) error {
		return errors.New("redis: connection refused")
	})
	h := readinessHandler(checker)
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/readiness", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready checker: got %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	body := w.Body.String()
	if !strings.Contains(body, "redis: connection refused") {
		t.Fatalf("not-ready body: got %q, want substring %q", body, "redis: connection refused")
	}
}

// TestReadinessHandlerTimeout verifies that a checker that exceeds the
// readinessCheckTimeout is cancelled and returns 503.
func TestReadinessHandlerTimeout(t *testing.T) {
	checker := ReadinessCheckerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	h := readinessHandler(checker)
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/readiness", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("timeout checker: got %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
