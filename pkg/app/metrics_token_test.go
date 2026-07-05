package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/observability"
)

// TestRegisterPublicMetrics_NoTokenIsOpen verifies that when no metrics token
// is configured, the /metrics endpoint is publicly accessible. This is the
// default and matches the behavior scrapers expect (no Authorization header).
func TestRegisterPublicMetrics_NoTokenIsOpen(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registry := observability.NewRegistry()
	RegisterPublicMetrics(mux, registry, "")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (open scrape)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type = %v, want text/plain", ct)
	}
}

// TestRegisterPublicMetrics_WithTokenRejectsMissingHeader verifies that when
// a token is configured, a scrape without an Authorization header is rejected
// with 401 and a Bearer challenge.
func TestRegisterPublicMetrics_WithTokenRejectsMissingHeader(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registry := observability.NewRegistry()
	RegisterPublicMetrics(mux, registry, "secret-token")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if www := rec.Header().Get("WWW-Authenticate"); !strings.Contains(www, "Bearer") {
		t.Fatalf("WWW-Authenticate = %q, want Bearer challenge", www)
	}
}

// TestRegisterPublicMetrics_WithTokenRejectsWrongToken verifies that a scrape
// with a non-matching token is rejected with 401. The constant-time compare
// prevents timing attacks from recovering the correct token.
func TestRegisterPublicMetrics_WithTokenRejectsWrongToken(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registry := observability.NewRegistry()
	RegisterPublicMetrics(mux, registry, "secret-token")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestRegisterPublicMetrics_WithTokenRejectsMalformedHeader verifies that a
// non-Bearer Authorization header is rejected. This prevents a caller from
// reusing a session token (which uses a different scheme) to scrape metrics.
func TestRegisterPublicMetrics_WithTokenRejectsMalformedHeader(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registry := observability.NewRegistry()
	RegisterPublicMetrics(mux, registry, "secret-token")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestRegisterPublicMetrics_WithTokenAcceptsCorrectToken verifies that a
// scrape with the correct Bearer token is allowed through to the underlying
// metrics handler.
func TestRegisterPublicMetrics_WithTokenAcceptsCorrectToken(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registry := observability.NewRegistry()
	RegisterPublicMetrics(mux, registry, "secret-token")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "process_goroutines") {
		t.Fatalf("metrics body missing process_goroutines: %s", rec.Body.String())
	}
}

// TestRegisterPublicMetrics_NilRegistryIsNoop verifies that a nil registry
// does not register any handler. This guards against panics in callers that
// pass nil (e.g., tests that don't care about metrics).
func TestRegisterPublicMetrics_NilRegistryIsNoop(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	RegisterPublicMetrics(mux, nil, "secret-token")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no handler registered)", rec.Code)
	}
}
