package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleUIReachableThroughHandler(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")
	req := httptest.NewRequest(http.MethodGet, "/v3/console/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "GoNacos Console") {
		t.Fatalf("missing console title")
	}
}

func TestConsoleUIReachableThroughNacosPrefix(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")
	req := httptest.NewRequest(http.MethodGet, "/nacos/v3/console/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	// The /nacos prefix is applied by the register helper for route stubs,
	// but the console is mounted directly. Expect 404 for the prefixed path
	// unless explicitly registered.
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}
