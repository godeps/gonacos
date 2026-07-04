package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleHandlerServesHTML(t *testing.T) {
	t.Parallel()
	handler := ConsoleHandler()
	req := httptest.NewRequest(http.MethodGet, "/v3/console/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %v", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatalf("missing doctype")
	}
	if !strings.Contains(body, "GoNacos Console") {
		t.Fatalf("missing title")
	}
	if !strings.Contains(body, "/v3/auth/user/login") {
		t.Fatalf("missing login API reference")
	}
	if !strings.Contains(body, "/v3/admin/ops/backup") {
		t.Fatalf("missing backup endpoint reference")
	}
}

func TestConsoleHandlerCacheNoCache(t *testing.T) {
	t.Parallel()
	handler := ConsoleHandler()
	req := httptest.NewRequest(http.MethodGet, "/v3/console/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("cache-control = %v, want no-cache", cc)
	}
}
