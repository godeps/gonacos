package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
