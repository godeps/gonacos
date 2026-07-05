package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler is a minimal handler that writes a 200 response so tests can
// verify the CORS middleware passes non-preflight requests through.
func corsOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// TestCORS_DisabledByDefault verifies that when Enabled is false the
// middleware is a no-op: no CORS headers are added and the request reaches
// the inner handler.
func TestCORS_DisabledByDefault(t *testing.T) {
	t.Parallel()
	handler := NewCORSMiddleware(CORSConfig{}, corsOKHandler())
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty", got)
	}
}

// TestCORS_WildcardOrigin verifies that a wildcard config echoes * back
// for any origin.
func TestCORS_WildcardOrigin(t *testing.T) {
	t.Parallel()
	handler := NewCORSMiddleware(CORSConfig{Enabled: true}, corsOKHandler())
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	req.Header.Set("Origin", "https://anywhere.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO = %q, want *", got)
	}
}

// TestCORS_ExplicitOriginAllowed verifies that a configured origin is
// echoed back when the request matches.
func TestCORS_ExplicitOriginAllowed(t *testing.T) {
	t.Parallel()
	handler := NewCORSMiddleware(CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"https://console.example.com"},
	}, corsOKHandler())
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	req.Header.Set("Origin", "https://console.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("ACAO = %q, want https://console.example.com", got)
	}
}

// TestCORS_OriginNotListed verifies that when credentials are off and the
// request origin is not in the configured list, no ACAO header is emitted.
func TestCORS_OriginNotListed(t *testing.T) {
	t.Parallel()
	handler := NewCORSMiddleware(CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"https://console.example.com"},
	}, corsOKHandler())
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	req.Header.Set("Origin", "https://attacker.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty for unlisted origin", got)
	}
}

// TestCORS_PreflightShortCircuits verifies that OPTIONS preflight requests
// get 204 with the right headers and never reach the inner handler.
func TestCORS_PreflightShortCircuits(t *testing.T) {
	t.Parallel()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	handler := NewCORSMiddleware(CORSConfig{Enabled: true}, inner)
	req := httptest.NewRequest(http.MethodOptions, "/v3/admin/cs/config", nil)
	req.Header.Set("Origin", "https://console.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type,Authorization")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if called {
		t.Fatalf("inner handler was called for preflight")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("ACAM missing")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatalf("ACAH missing")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Fatalf("ACMA missing")
	}
}

// TestCORS_OptionsWithoutPreflightHeaders verifies that a plain OPTIONS
// request (no Access-Control-Request-Method) is delegated to the inner
// handler rather than short-circuited.
func TestCORS_OptionsWithoutPreflightHeaders(t *testing.T) {
	t.Parallel()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := NewCORSMiddleware(CORSConfig{Enabled: true}, inner)
	req := httptest.NewRequest(http.MethodOptions, "/v3/admin/cs/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("inner handler not called for plain OPTIONS")
	}
}

// TestCORS_CredentialsWithWildcard verifies that when AllowCredentials is
// true and origins are wildcard (the default), no ACAO is emitted (browsers
// reject "*" with credentials). ACAC is also withheld because no origin
// matched — the response carries no CORS policy at all.
func TestCORS_CredentialsWithWildcard(t *testing.T) {
	t.Parallel()
	handler := NewCORSMiddleware(CORSConfig{
		Enabled:          true,
		AllowCredentials: true,
	}, corsOKHandler())
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty (wildcard+credentials invalid)", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("ACAC = %q, want empty when no origin matched", got)
	}
}

// TestCORS_CredentialsWithExplicitOrigin verifies that credentials+explicit
// origin echoes the origin back (not "*") and sets Allow-Credentials: true.
func TestCORS_CredentialsWithExplicitOrigin(t *testing.T) {
	t.Parallel()
	handler := NewCORSMiddleware(CORSConfig{
		Enabled:          true,
		AllowOrigins:     []string{"https://console.example.com"},
		AllowCredentials: true,
	}, corsOKHandler())
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	req.Header.Set("Origin", "https://console.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("ACAO = %q, want https://console.example.com", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("ACAC = %q, want true", got)
	}
}

// TestCORS_NoOriginHeader verifies that when the request has no Origin
// header (same-origin browser request, or curl), no CORS headers are
// emitted. This avoids leaking CORS policy to non-cross-origin callers.
func TestCORS_NoOriginHeader(t *testing.T) {
	t.Parallel()
	handler := NewCORSMiddleware(CORSConfig{Enabled: true}, corsOKHandler())
	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty for same-origin request", got)
	}
}
