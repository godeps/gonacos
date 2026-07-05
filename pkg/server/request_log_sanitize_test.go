package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSanitizeRequestURIRedactsAccessToken verifies that the accessToken
// query parameter — which the Nacos SDK passes on GET requests and
// auth_middleware.go reads from r.URL.Query() — is redacted before the
// URI is written to the access log.
//
// Without redaction, a single leaked access log line is equivalent to
// a session hijack: the attacker replays the token and impersonates the
// user until expiry.
func TestSanitizeRequestURIRedactsAccessToken(t *testing.T) {
	cases := []struct {
		name        string
		uri         string
		mustContain string
		mustNot     string
	}{
		{
			name:        "accessToken camelCase",
			uri:         "/v3/cs/configs?accessToken=secret-token-xyz&page=1",
			mustContain: "accessToken=***",
			mustNot:     "secret-token-xyz",
		},
		{
			name:        "accessToken uppercase",
			uri:         "/v3/cs/configs?ACCESSTOKEN=secret-token-xyz",
			mustContain: "ACCESSTOKEN=***",
			mustNot:     "secret-token-xyz",
		},
		{
			name:        "password",
			uri:         "/v3/auth/user/login?password=hunter2",
			mustContain: "password=***",
			mustNot:     "hunter2",
		},
		{
			name:        "secret",
			uri:         "/v3/cs/configs?secret=blob&dataId=foo",
			mustContain: "secret=***",
			mustNot:     "blob",
		},
		{
			name:        "api_key snake_case",
			uri:         "/v1/api?api_key=AKIA123&resource=bar",
			mustContain: "api_key=***",
			mustNot:     "AKIA123",
		},
		{
			name:        "apikey no underscore",
			uri:         "/v1/api?apikey=AKIA123",
			mustContain: "apikey=***",
			mustNot:     "AKIA123",
		},
		{
			name:        "multiple sensitive keys",
			uri:         "/x?accessToken=t1&password=p1&dataId=foo",
			mustContain: "dataId=foo",
			mustNot:     "t1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.uri, nil)
			got := sanitizeRequestURI(req.URL)
			if !strings.Contains(got, c.mustContain) {
				t.Errorf("sanitizeRequestURI = %q, want substring %q", got, c.mustContain)
			}
			if strings.Contains(got, c.mustNot) {
				t.Errorf("sanitizeRequestURI = %q, must NOT contain %q (secret leaked)", got, c.mustNot)
			}
		})
	}
}

// TestSanitizeRequestURIPreservesNonSensitive verifies that non-sensitive
// query parameters pass through unchanged so operators can still debug
// requests by dataId/group/namespace.
func TestSanitizeRequestURIPreservesNonSensitive(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs?dataId=app.yml&group=DEFAULT&namespaceId=public&page=1", nil)
	got := sanitizeRequestURI(req.URL)
	for _, want := range []string{"dataId=app.yml", "group=DEFAULT", "namespaceId=public", "page=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitizeRequestURI = %q, want substring %q (non-sensitive should pass through)", got, want)
		}
	}
}

// TestSanitizeRequestURINoQuery verifies that a URL with no query
// string passes through unchanged.
func TestSanitizeRequestURINoQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs", nil)
	got := sanitizeRequestURI(req.URL)
	if got != "/v3/cs/configs" {
		t.Errorf("sanitizeRequestURI = %q, want %q", got, "/v3/cs/configs")
	}
}

// TestSanitizeRequestURIDoesNotMutateOriginal verifies that the original
// r.URL still carries the real query parameters after sanitization —
// the handler downstream of the logger must still see the real token
// to authenticate the request.
func TestSanitizeRequestURIDoesNotMutateOriginal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs?accessToken=real-token", nil)
	_ = sanitizeRequestURI(req.URL)
	if got := req.URL.Query().Get("accessToken"); got != "real-token" {
		t.Errorf("original URL mutated: accessToken = %q, want %q (handler must see real token)", got, "real-token")
	}
}

// TestRequestLogMiddlewareRedactsAccessTokenInURI verifies end-to-end that
// the request log middleware does not write the accessToken value to the
// log buffer, even when the SDK passes it as a query parameter.
func TestRequestLogMiddlewareRedactsAccessTokenInURI(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler sees the real token (not redacted).
		if got := r.URL.Query().Get("accessToken"); got != "real-token-abc" {
			t.Errorf("handler saw wrong accessToken: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})
	mw := newRequestLogMiddleware(logger, false, nil, inner)

	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs?accessToken=real-token-abc&dataId=foo", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	logLine := buf.String()
	if strings.Contains(logLine, "real-token-abc") {
		t.Errorf("access token leaked to log: %q", logLine)
	}
	if !strings.Contains(logLine, "accessToken=***") {
		t.Errorf("redaction marker missing in log: %q", logLine)
	}
	if !strings.Contains(logLine, "dataId=foo") {
		t.Errorf("non-sensitive param dropped from log: %q", logLine)
	}
}

// TestRecoveryMiddlewareRedactsAccessTokenInPanicLog verifies that the
// panic recovery middleware does not leak the accessToken to the panic
// log line — panics are rare but the recovery log carries the full URL,
// so the same redaction must apply.
func TestRecoveryMiddlewareRedactsAccessTokenInPanicLog(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	mw := newRecoveryMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs?accessToken=panic-token-xyz", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	logLine := buf.String()
	if strings.Contains(logLine, "panic-token-xyz") {
		t.Errorf("access token leaked to panic log: %q", logLine)
	}
	if !strings.Contains(logLine, "accessToken=***") {
		t.Errorf("redaction marker missing in panic log: %q", logLine)
	}
}
