package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersSetsDefaults(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := NewSecurityHeadersMiddleware(false, inner)

	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	cases := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"X-XSS-Protection":       "0",
	}
	for h, want := range cases {
		if got := w.Header().Get(h); got != want {
			t.Errorf("header %s: got %q, want %q", h, got, want)
		}
	}
	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS must NOT be set on plaintext (tls=false): got %q", got)
	}
}

func TestSecurityHeadersHSTSOnlyOnTLS(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := NewSecurityHeadersMiddleware(true, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if got := w.Header().Get("Strict-Transport-Security"); got == "" {
		t.Fatal("HSTS header missing when tls=true")
	}
}

func TestSecurityHeadersInnerHandlerCanOverride(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.WriteHeader(http.StatusOK)
	})
	mw := NewSecurityHeadersMiddleware(false, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("inner handler override lost: got %q, want DENY", got)
	}
}
